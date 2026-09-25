package fleetbuild

// compile.go is the builder half of `nova-sprint fleet build` (#4080): the
// release build that rowan-tools' space-build script ran on space as a bash
// heredoc, in Go, run ON the builder as `nova-sprint fleet build compile`.
//
// THE HURT (2026-09-25). space is both the release builder and a CI runner.
// The release build ran with the bench user's Go caches ($HOME/go/pkg/mod and
// $HOME/.cache/go-build) and GOTOOLCHAIN=go1.27.1, so it extracted the go1.27.1
// toolchain into the same module cache the runner's jobs read while they ran
// the sdk Go 1.26.6. Three CI jobs failed inside the toolchain that day
// ("package crypto/internal/fips140/rsa is not in std", "package iter is not in
// std", a build-cache file missing), each during a release build.
//
// THE FIX. The build runs with its own GOMODCACHE and GOCACHE under
// <home>/nova-bench/space-build/go/ (GoEnv); GOTOOLCHAIN names the toolchain
// the reference build was made with, and a toolchain it has to download lands
// in that same module cache, so the extraction is the build's own too. Every
// go child gets that environment on top of goenv.Clean, and the BUILT and
// REFERENCE BUILT lines name the caches they used.
//
// The steps and their lines are space-build's, unchanged in meaning:
//
//  1. a platform whose <home>/nova-bench/release/<v>/<p>/SHA256SUMS already
//     verifies is OK and not rebuilt;
//  2. --dry-run stops here with one WOULD line per platform still to build;
//  3. <home>/nova-bench/space-build/src/nova-tools fetches the commit and the
//     v* tags from the repo URL (GitHub; the local mirror is only an object
//     alternate, never shallow, so the pseudo-version is stamped from whole
//     history) and checks it out clean;
//  3b. when there is no linux-amd64 reference at <home>/nova-bench/build/<v>,
//     it is built from the same checkout with the step-5 recipe, the toolchain
//     read from the newest existing reference (else the go on PATH), into
//     <v>.tmp and renamed into place, never over anything;
//  4. GOTOOLCHAIN is the toolchain the reference was made with;
//  5. every cmd/nova-* is compiled for every platform still to build into
//     <home>/nova-bench/space-build/out/<v>/<p>/ (CGO_ENABLED=0 go build
//     -trimpath -ldflags "-X main.version=<v>"), with a SHA256SUMS per platform;
//  6. every file carries its platform's binary header;
//  7. THE IDENTICAL-DIGEST RULE: the linux-amd64 build has the reference's file
//     names and sha256s, file for file;
//  8. each platform directory is renamed into the release root (never over an
//     existing one) and verified there.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	// WorkDir holds the checkout, the unpublished output and the logs.
	WorkDir = "nova-bench/space-build"
	// GoDir is the build's own Go state: GOMODCACHE at mod/ (with any toolchain
	// GOTOOLCHAIN downloads) and GOCACHE at build/. Nothing else uses it.
	GoDir = WorkDir + "/go"
	// ReferenceRoot holds the linux-amd64 reference build of each version.
	ReferenceRoot = "nova-bench/build"
	// MirrorDir is the local object alternate for the checkout.
	MirrorDir = "nova-bench/mirror/nova-tools.git"
	// DefaultRepoURL is where the commit is fetched from.
	DefaultRepoURL = "https://github.com/mas-bandwidth/nova-tools.git"
	// DefaultPlatforms is every bench platform in the fleet today.
	DefaultPlatforms = "linux-amd64,darwin-arm64,darwin-amd64"
)

var (
	urlRe       = regexp.MustCompile(`^[A-Za-z0-9._:/@+-]+$`)
	toolchainRe = regexp.MustCompile(`^go[0-9]+\.[0-9]+(\.[0-9]+)?$`)
)

// GoEnv is the environment every go child of the release build gets on top of
// goenv.Clean: its own module cache and build cache under <home>/GoDir, and
// GOTOOLCHAIN pinned to toolchain ("local" when none was read).
func GoEnv(home, toolchain string) []string {
	if toolchain == "" {
		toolchain = "local"
	}
	return []string{
		"GOMODCACHE=" + GoModCache(home),
		"GOCACHE=" + GoBuildCache(home),
		"GOTOOLCHAIN=" + toolchain,
	}
}

// GoModCache is the release build's GOMODCACHE.
func GoModCache(home string) string { return filepath.Join(home, GoDir, "mod") }

// GoBuildCache is the release build's GOCACHE.
func GoBuildCache(home string) string { return filepath.Join(home, GoDir, "build") }

// Exec starts one local child (git or go) in dir with extra appended to the
// sanitized environment, and returns its combined output. Production execs;
// tests fake it.
type Exec func(ctx context.Context, dir string, extra []string, argv []string) (string, error)

// Compile is one run of the builder half.
type Compile struct {
	Home      string
	Version   string
	Commit    string
	Platforms []string
	RepoURL   string
	DryRun    bool
	Exec      Exec
	// Toolchain reads the Go version a binary was built with; nil reads its
	// build info.
	Toolchain func(path string) (string, error)
	Out       io.Writer
}

func (c *Compile) say(format string, a ...any) {
	if c.Out != nil {
		fmt.Fprintf(c.Out, format+"\n", a...)
	}
}

func (c *Compile) toolchainOf(path string) string {
	read := c.Toolchain
	if read == nil {
		read = func(p string) (string, error) {
			bi, err := buildinfo.ReadFile(p)
			if err != nil {
				return "", err
			}
			return bi.GoVersion, nil
		}
	}
	v, err := read(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

func (c *Compile) validate() error {
	sm := versionRe.FindStringSubmatch(c.Version)
	switch {
	case !filepath.IsAbs(c.Home) || strings.Count(filepath.Clean(c.Home), string(filepath.Separator)) < 2:
		return refused("bad home %q on the builder", c.Home)
	case sm == nil:
		return refused("version %q is not v<x>.<y>.<z>-dev.<sha8>", c.Version)
	case !commitRe.MatchString(c.Commit):
		return refused("commit %q is not a full 40-hex sha", c.Commit)
	case !strings.HasPrefix(c.Commit, sm[1]):
		return refused("commit %s is not the version's sha %s", c.Commit, sm[1])
	case !urlRe.MatchString(c.RepoURL):
		return refused("repo url %q", c.RepoURL)
	case len(c.Platforms) == 0:
		return refused("no platform")
	case c.Exec == nil:
		return refused("no child runner")
	}
	for _, p := range c.Platforms {
		if !platformRe.MatchString(p) {
			return refused("platform %q (linux-amd64, linux-arm64, darwin-arm64, darwin-amd64)", p)
		}
	}
	return nil
}

// Run builds and publishes the platforms not yet published. A refusal wraps
// ErrRefused and names the remedy; the last line printed on success is
// FLEET COMPILE OK.
func (c *Compile) Run(ctx context.Context) error {
	if err := c.validate(); err != nil {
		return err
	}
	var (
		v    = c.Version
		work = filepath.Join(c.Home, WorkDir)
		pub  = filepath.Join(c.Home, ReleaseRoot, v)
		ref  = filepath.Join(c.Home, ReferenceRoot, v)
		all  = strings.Join(c.Platforms, ",")
	)

	// 1-2. what is already published, and what is still to build
	var todo []string
	for _, p := range c.Platforms {
		d := filepath.Join(pub, p)
		if !exists(d) {
			todo = append(todo, p)
			continue
		}
		if err := verifyDir(d); err != nil {
			return refused("%s already holds files that do not verify against their SHA256SUMS: %v (never overwritten; remedy: move it aside by hand and rerun)", d, err)
		}
		c.say("OK %s %s sums=%s", p, d, sumFile(filepath.Join(d, "SHA256SUMS")))
	}
	if len(todo) == 0 {
		c.say("FLEET COMPILE OK %s commit=%s platforms=%s built=none", v, c.Commit, all)
		return nil
	}
	if c.DryRun {
		if !exists(ref) {
			c.say("WOULD reference linux-amd64 build %s at %s from %s into %s", v, c.Commit, c.RepoURL, ref)
		}
		for _, p := range todo {
			c.say("WOULD %s build %s at %s from %s into %s go=%s", p, v, c.Commit, c.RepoURL, filepath.Join(pub, p), filepath.Join(c.Home, GoDir))
		}
		return nil
	}

	// 3. the declared commit, whole history
	src := filepath.Join(work, "src", "nova-tools")
	tools, err := c.checkout(ctx, src)
	if err != nil {
		return err
	}

	// 3b. the linux-amd64 reference, built here when there is none
	if !exists(ref) {
		if err := c.reference(ctx, src, ref, tools); err != nil {
			return err
		}
	} else if !isDir(ref) {
		return refused("%s exists and is not a directory", ref)
	}

	// 4. the toolchain the reference was made with
	tc := ""
	if f := firstTool(ref); f != "" {
		tc = c.toolchainOf(f)
		if tc != "" && !toolchainRe.MatchString(tc) {
			return refused("toolchain %q read from %s", tc, ref)
		}
	}

	// 5. every platform still to build
	out := filepath.Join(work, "out", v)
	if exists(out) {
		return refused("%s is left from an earlier run (remedy: move it aside by hand and rerun)", out)
	}
	logPath := filepath.Join(work, "build-"+v+".log")
	log, err := os.Create(logPath)
	if err != nil {
		return refused("cannot write %s: %v", logPath, err)
	}
	defer log.Close()
	for _, p := range todo {
		d := filepath.Join(out, p)
		if err := os.MkdirAll(d, 0o755); err != nil {
			return refused("cannot make %s: %v", d, err)
		}
		for _, t := range tools {
			if tail, err := c.goBuild(ctx, src, p, tc, filepath.Join(d, t), t, log); err != nil {
				return refused("go build %s for %s failed: %v: %s (log %s; output left in %s)", t, p, err, tail, logPath, out)
			}
		}
		if err := writeSums(d); err != nil {
			return refused("SHA256SUMS for %s: %v", p, err)
		}
	}
	c.say("BUILT %s platforms=%s tools=%d toolchain=%s gomodcache=%s gocache=%s log=%s",
		v, strings.Join(todo, ","), len(tools), orDefault(tc), GoModCache(c.Home), GoBuildCache(c.Home), logPath)

	// 6. every file carries its platform's binary header
	for _, p := range todo {
		d := filepath.Join(out, p)
		if err := verifyDir(d); err != nil {
			return refused("%s does not verify against its own SHA256SUMS: %v", d, err)
		}
		names := toolFiles(d)
		if len(names) == 0 {
			return refused("%s holds no nova-* file", d)
		}
		for _, n := range names {
			if !headerOK(p, filepath.Join(d, n)) {
				return refused("%s file %s is not %s binary (built output left in %s)", p, n, wantHeader(p), out)
			}
		}
	}

	// 7. the identical-digest rule, on the linux-amd64 build
	if d := filepath.Join(out, "linux-amd64"); isDir(d) {
		refNames, newNames := toolFiles(ref), toolFiles(d)
		if strings.Join(refNames, " ") != strings.Join(newNames, " ") {
			return refused("linux-amd64 file names are not identical to %s (built: %s; reference: %s)", ref, strings.Join(newNames, " "), strings.Join(refNames, " "))
		}
		for _, n := range newNames {
			if sumFile(filepath.Join(d, n)) != sumFile(filepath.Join(ref, n)) {
				return refused("linux-amd64 %s is not identical to %s (sha256 differs: not the declared build; output left in %s)", n, filepath.Join(ref, n), out)
			}
		}
		c.say("IDENTICAL linux-amd64 files=%d reference=%s", len(newNames), ref)
	} else {
		if !isDir(filepath.Join(pub, "linux-amd64")) {
			return refused("no linux-amd64 build in this run or in %s to hold the digest rule to (remedy: include linux-amd64 in --platform)", pub)
		}
		c.say("IDENTICAL linux-amd64 published-earlier=%s", filepath.Join(pub, "linux-amd64"))
	}

	// 8. publish: one rename per platform, never over an existing directory
	if err := os.MkdirAll(pub, 0o755); err != nil {
		return refused("cannot make %s: %v", pub, err)
	}
	for _, p := range todo {
		d := filepath.Join(pub, p)
		if exists(d) {
			return refused("%s appeared during the build (not overwritten)", d)
		}
		if err := os.Rename(filepath.Join(out, p), d); err != nil {
			return refused("rename into %s: %v", d, err)
		}
		if err := verifyDir(d); err != nil {
			return refused("%s does not verify after the rename: %v", d, err)
		}
		c.say("PUBLISHED %s files=%d sums=%s dir=%s", p, len(toolFiles(d)), sumFile(filepath.Join(d, "SHA256SUMS")), d)
	}
	_ = os.Remove(out) // the emptied out/<v>; never a recursive remove
	c.say("FLEET COMPILE OK %s commit=%s platforms=%s built=%s go=%s", v, c.Commit, all, strings.Join(todo, ","), filepath.Join(c.Home, GoDir))
	return nil
}

// checkout fetches the commit and the v* tags into src and checks it out
// clean; it returns the cmd/nova-* tools there.
func (c *Compile) checkout(ctx context.Context, src string) ([]string, error) {
	git := func(args ...string) (string, error) {
		return c.Exec(ctx, "", nil, append([]string{"git"}, args...))
	}
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		return nil, refused("cannot make %s: %v", filepath.Dir(src), err)
	}
	if !exists(filepath.Join(src, ".git")) {
		if out, err := git("init", "-q", src); err != nil {
			return nil, refused("git init %s: %v: %s", src, err, lastLine(out))
		}
	}
	if exists(filepath.Join(src, ".git", "shallow")) {
		return nil, refused("%s is a shallow repository (the pseudo-version needs whole history)", src)
	}
	mirror := filepath.Join(c.Home, MirrorDir)
	if isDir(filepath.Join(mirror, "objects")) && !exists(filepath.Join(mirror, "shallow")) {
		alt := filepath.Join(src, ".git", "objects", "info", "alternates")
		if err := os.MkdirAll(filepath.Dir(alt), 0o755); err == nil {
			_ = os.WriteFile(alt, []byte(filepath.Join(mirror, "objects")+"\n"), 0o644)
		}
	}
	if out, err := git("-C", src, "fetch", "-q", "--no-tags", c.RepoURL, c.Commit, "+refs/tags/v*:refs/tags/v*"); err != nil {
		return nil, refused("fetch %s from %s: %v: %s", c.Commit, c.RepoURL, err, lastLine(out))
	}
	if out, err := git("-C", src, "-c", "advice.detachedHead=false", "checkout", "-q", "--force", "--detach", c.Commit); err != nil {
		return nil, refused("checkout %s: %v: %s", c.Commit, err, lastLine(out))
	}
	if out, err := git("-C", src, "rev-parse", "HEAD"); err != nil || strings.TrimSpace(out) != c.Commit {
		return nil, refused("checkout is not %s (HEAD %s)", c.Commit, lastLine(out))
	}
	if out, err := git("-C", src, "status", "--porcelain"); err != nil || strings.TrimSpace(out) != "" {
		return nil, refused("%s is not clean at %s", src, c.Commit)
	}
	ents, _ := os.ReadDir(filepath.Join(src, "cmd"))
	var tools []string
	for _, e := range ents {
		if e.IsDir() && strings.HasPrefix(e.Name(), "nova-") {
			tools = append(tools, e.Name())
		}
	}
	if len(tools) == 0 {
		return nil, refused("no cmd/nova-* in %s at %s", src, c.Commit)
	}
	return tools, nil
}

// reference builds the linux-amd64 reference of the version into ref.
func (c *Compile) reference(ctx context.Context, src, ref string, tools []string) error {
	tmp := ref + ".tmp"
	if exists(tmp) {
		return refused("%s is left from an earlier reference build (remedy: move it aside by hand and rerun)", tmp)
	}
	rtc, from := "", "go on PATH"
	if newest := newestReference(filepath.Dir(ref)); newest != "" {
		if f := firstTool(newest); f != "" {
			rtc, from = c.toolchainOf(f), f
		}
	}
	if rtc == "" {
		from = "go on PATH"
		if out, err := c.Exec(ctx, c.Home, GoEnv(c.Home, "local"), []string{"go", "env", "GOVERSION"}); err == nil {
			rtc = strings.TrimSpace(out)
		}
	}
	if rtc != "" && !toolchainRe.MatchString(rtc) {
		return refused("reference toolchain %q read from %s", rtc, from)
	}
	rlogPath := filepath.Join(c.Home, WorkDir, "reference-"+c.Version+".log")
	rlog, err := os.Create(rlogPath)
	if err != nil {
		return refused("cannot write %s: %v", rlogPath, err)
	}
	defer rlog.Close()
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return refused("cannot make %s: %v", tmp, err)
	}
	for _, t := range tools {
		if tail, err := c.goBuild(ctx, src, "linux-amd64", rtc, filepath.Join(tmp, t), t, rlog); err != nil {
			return refused("reference go build %s for linux-amd64 failed: %v: %s (log %s; output left in %s)", t, err, tail, rlogPath, tmp)
		}
	}
	if exists(ref) {
		return refused("%s appeared during the reference build (not overwritten; %s left)", ref, tmp)
	}
	if err := os.Rename(tmp, ref); err != nil {
		return refused("rename %s into %s: %v", tmp, ref, err)
	}
	c.say("REFERENCE BUILT linux-amd64 %s files=%d toolchain=%s from=%s dir=%s gomodcache=%s gocache=%s",
		c.Version, len(toolFiles(ref)), orDefault(rtc), from, ref, GoModCache(c.Home), GoBuildCache(c.Home))
	return nil
}

// BuildEnv is the whole environment one go build child gets on top of
// goenv.Clean: the build's own caches and toolchain, cgo off, the platform.
func BuildEnv(home, toolchain, platform string) []string {
	goos, goarch, _ := strings.Cut(platform, "-")
	return append(GoEnv(home, toolchain), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
}

// goBuild compiles one tool for one platform, its output appended to log.
func (c *Compile) goBuild(ctx context.Context, src, platform, tc, out, tool string, log io.Writer) (string, error) {
	argv := []string{"go", "build", "-trimpath", "-ldflags", "-X main.version=" + c.Version, "-o", out, "./cmd/" + tool}
	o, err := c.Exec(ctx, src, BuildEnv(c.Home, tc, platform), argv)
	_, _ = io.WriteString(log, o)
	return lastLine(o), err
}

func orDefault(tc string) string {
	if tc == "" {
		return "local"
	}
	return tc
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func isDir(p string) bool { fi, err := os.Stat(p); return err == nil && fi.IsDir() }

// toolFiles are the regular nova-* files in d, sorted.
func toolFiles(d string) []string {
	ents, _ := os.ReadDir(d)
	var names []string
	for _, e := range ents {
		if e.Type().IsRegular() && strings.HasPrefix(e.Name(), "nova-") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func firstTool(d string) string {
	if n := toolFiles(d); len(n) > 0 {
		return filepath.Join(d, n[0])
	}
	return ""
}

// newestReference is the most recently modified v* directory under root,
// leaving out *.tmp.
func newestReference(root string) string {
	ents, _ := os.ReadDir(root)
	var best string
	var bestT int64
	for _, e := range ents {
		n := e.Name()
		if !strings.HasPrefix(n, "v") || strings.HasSuffix(n, ".tmp") {
			continue
		}
		fi, err := os.Stat(filepath.Join(root, n))
		if err != nil || !fi.IsDir() {
			continue
		}
		if t := fi.ModTime().UnixNano(); best == "" || t > bestT {
			best, bestT = filepath.Join(root, n), t
		}
	}
	return best
}

func sumFile(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// writeSums writes d/SHA256SUMS: one "<sha256>  <name>" line per nova-* file.
func writeSums(d string) error {
	var b strings.Builder
	for _, n := range toolFiles(d) {
		fmt.Fprintf(&b, "%s  %s\n", sumFile(filepath.Join(d, n)), n)
	}
	return os.WriteFile(filepath.Join(d, "SHA256SUMS"), []byte(b.String()), 0o644)
}

// verifyDir checks every line of d/SHA256SUMS against its file.
func verifyDir(d string) error {
	raw, err := os.ReadFile(filepath.Join(d, "SHA256SUMS"))
	if err != nil {
		return err
	}
	n := 0
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		want, name, ok := strings.Cut(line, "  ")
		if !ok || name == "" || strings.ContainsRune(name, '/') {
			return fmt.Errorf("SHA256SUMS line %q", line)
		}
		if got := sumFile(filepath.Join(d, name)); got != want {
			return fmt.Errorf("%s: sha256 %q, SHA256SUMS says %s", name, got, want)
		}
		n++
	}
	if n == 0 {
		return errors.New("SHA256SUMS is empty")
	}
	return nil
}

// headerOK reports whether the file starts with its platform's binary header:
// ELF with e_machine x86-64 or aarch64, or 64-bit Mach-O arm64 or x86-64.
func headerOK(platform, path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := make([]byte, 20)
	if _, err := io.ReadFull(f, h); err != nil {
		return false
	}
	elf := []byte{0x7f, 'E', 'L', 'F'}
	switch platform {
	case "linux-amd64":
		return bytes.Equal(h[:4], elf) && h[18] == 0x3e && h[19] == 0
	case "linux-arm64":
		return bytes.Equal(h[:4], elf) && h[18] == 0xb7 && h[19] == 0
	case "darwin-arm64":
		return bytes.Equal(h[:8], []byte{0xcf, 0xfa, 0xed, 0xfe, 0x0c, 0, 0, 0x01})
	case "darwin-amd64":
		return bytes.Equal(h[:8], []byte{0xcf, 0xfa, 0xed, 0xfe, 0x07, 0, 0, 0x01})
	}
	return false
}

func wantHeader(p string) string {
	return map[string]string{"linux-amd64": "an ELF x86-64", "linux-arm64": "an ELF aarch64",
		"darwin-arm64": "a Mach-O arm64", "darwin-amd64": "a Mach-O x86-64"}[p]
}
