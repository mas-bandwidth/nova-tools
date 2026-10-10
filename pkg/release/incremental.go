package release

// THE INCREMENTAL BUILD (nova-tools#5096 item 12, 2026-10-02).
//
// A fix to one binary during a sprint cost a 25-minute fix-land-install cycle,
// and part of it was `release build` relinking and restamping all eighteen
// tools for every platform when two had changed. Worse, every restamped binary
// differed in its bytes from the one on the bench, so the play copied all of
// them and every loop on every bench drained and restarted on a binary nothing
// had changed in.
//
// `release build --incremental` compiles only the tools whose packages, or the
// packages they import, differ between the commit an earlier build under --out
// recorded and the checkout now; every other tool is that earlier build's
// binary, byte for byte, verified against its SHA256SUMS before it is copied.
// A reused binary answers the version it was built at, which is the truth
// about its bytes. The question is a TREE diff (`git diff --name-only
// --no-renames <recorded> <head>`), so any earlier clean build is a correct
// base whatever branch it came from, and a rename is two paths, never one.
//
// It is never a guess in the direction that ships stale code: a dirty checkout,
// a different Go, a different stamp shape, a go.mod or go.sum change, a tool the
// base lacks, or a tool `go list` did not answer for is a rebuild, and the line
// says why. `cut` never builds this way: a tagged release is built whole.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/bounded"
	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// IncrementalNote is --incremental and --gate report said where a person meets them.
const IncrementalNote = "build writes a record beside each platform directory, <out>/<version>/<goos-goarch>" + RecordSuffix + ": the commit (none when the checkout is dirty), the Go, the stamp shape, the gate and its reason. " +
	"--incremental compiles only the tools whose packages, or the packages they import, differ (`git diff --name-only --no-renames`) between the newest such record under --out and the checkout, and copies every other tool from that build, verified against its SHA256SUMS; " +
	"a reused tool answers the version it was built at. A dirty checkout, another Go, a go.mod or go.sum change, or no record is a whole build, said on RELEASE BUILD WHOLE; otherwise RELEASE BUILD INCREMENTAL names the base and every tool rebuilt. " +
	"--gate report --reason <why> runs the dogfood gate, prints its open edges and RELEASE BUILD DOGFOOD REPORTED, and builds (dogfood=report); it is for a machinery install during a sprint, and cut has no such flag."

// RecordSuffix names the build record written beside each platform directory,
// <out>/<version>/<goos-goarch>.build. It sits OUTSIDE the platform directory
// so that it is never in SHA256SUMS, never copied to a bench and never
// installed: it is this host's note about how the artifacts were made.
const RecordSuffix = ".build"

// recordPath is where one platform's build record lives.
func recordPath(out, version, platform string) string {
	return filepath.Join(out, version, platform+RecordSuffix)
}

// buildRecord is what a build says about itself, so a later build can ask
// what changed since. Commit is empty when the checkout was dirty or not a git
// checkout, and such a record is never a base.
type buildRecord struct {
	Commit, Go, Ldflags, Base, Gate, Reason string
	Rebuilt, Reused                         []string
}

// ldflagsShape is the stamp's shape with the version taken out, so two builds
// stamping differently (a new -X) are never each other's base.
func ldflagsShape() string { return Ldflags("{version}") }

func (r buildRecord) encode() []byte {
	var b strings.Builder
	for _, kv := range [][2]string{
		{"commit", r.Commit}, {"go", r.Go}, {"ldflags", r.Ldflags}, {"base", r.Base},
		{"rebuilt", strings.Join(r.Rebuilt, ",")}, {"reused", strings.Join(r.Reused, ",")},
		{"gate", r.Gate}, {"reason", r.Reason},
	} {
		fmt.Fprintf(&b, "%s=%s\n", kv[0], strings.Join(strings.Fields(kv[1]), " "))
	}
	return []byte(b.String())
}

func readRecord(path string) (buildRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return buildRecord{}, err
	}
	// ignored: the record file was opened only for reading; the scanner's error is the one returned
	defer func() { _ = f.Close() }()
	var r buildRecord
	s := bufio.NewScanner(f)
	for s.Scan() {
		k, v, _ := strings.Cut(s.Text(), "=")
		switch k {
		case "commit":
			r.Commit = v
		case "go":
			r.Go = v
		case "ldflags":
			r.Ldflags = v
		case "base":
			r.Base = v
		}
	}
	return r, s.Err()
}

// Source is the edge an incremental build asks: where the checkout is, what
// changed since a recorded commit, which package directories each tool is
// built from, and which Go builds it. A nil Source in Deps is ExecSource.
type Source interface {
	// Head is the commit the checkout is at and whether its tree is clean:
	// no change to a tracked file and no untracked file.
	Head(ctx context.Context, dir string) (commit string, clean bool, err error)
	// Changed is every path that differs between two commits' trees.
	Changed(ctx context.Context, dir, base, head string) ([]string, error)
	// Packages answers, for one platform, each package's directories relative
	// to dir: its own and every non-standard package it imports, transitively.
	Packages(ctx context.Context, dir, goos, goarch string, pkgs []string) (map[string][]string, error)
	// GoVersion is the version of the go that builds the release.
	GoVersion(ctx context.Context) (string, error)
}

// rebuildSet decides one platform's build: which tools compile and which are
// copied from the base. dirs is Packages' answer keyed by tool; inBase says
// whether the base holds that tool's file. why names each rebuild's reason.
func rebuildSet(tools []string, dirs map[string][]string, changed []string, inBase func(string) bool) (rebuild, reuse []string, why map[string]string) {
	why = map[string]string{}
	var code []string
	all := ""
	for _, p := range changed {
		if strings.HasSuffix(p, "_test.go") {
			continue // a test is not in any binary
		}
		if p == "go.mod" || p == "go.sum" || p == "go.work" || p == "go.work.sum" {
			all = p
		}
		code = append(code, p)
	}
	for _, tool := range tools {
		d, known := dirs[tool]
		switch {
		case !inBase(tool):
			why[tool] = "new"
		case all != "":
			why[tool] = all
		case !known:
			why[tool] = "unlisted"
		default:
			for _, p := range code {
				if i := slices.IndexFunc(d, func(dir string) bool { return dir == "." || strings.HasPrefix(p, dir+"/") }); i >= 0 {
					why[tool] = p
					break
				}
			}
		}
		if why[tool] != "" {
			rebuild = append(rebuild, tool)
		} else {
			reuse = append(reuse, tool)
		}
	}
	return rebuild, reuse, why
}

// plan is one platform's incremental decision as `build` acts on it.
type plan struct {
	base    string     // the version reused from, "" for a whole build
	baseDir string     // its artifact directory
	sums    []Artifact // its verified SHA256SUMS
	rebuild []string
	reuse   []string
	changed int
	note    string // why the build is whole, when it is
}

// findBase is the newest version under out, other than version, whose record
// for this platform names a clean commit built by the same Go with the same
// stamp shape and whose SHA256SUMS is there to verify against.
func findBase(out, version, platform, goVersion string) (string, buildRecord) {
	entries, err := os.ReadDir(out)
	if err != nil {
		return "", buildRecord{}
	}
	var best string
	var bestRec buildRecord
	var bestMod int64
	for _, e := range entries {
		if !e.IsDir() || e.Name() == version || ValidVersion(e.Name()) != nil {
			continue
		}
		path := recordPath(out, e.Name(), platform)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		rec, err := readRecord(path)
		if err != nil || rec.Commit == "" || rec.Go != goVersion || rec.Ldflags != ldflagsShape() {
			continue
		}
		if !exists(filepath.Join(out, e.Name(), platform, SumsFile)) {
			continue
		}
		if m := info.ModTime().UnixNano(); best == "" || m > bestMod {
			best, bestRec, bestMod = e.Name(), rec, m
		}
	}
	return best, bestRec
}

// planIncremental answers one platform's plan. Any reason it cannot reuse is a
// whole build with the reason in note, never a refusal: the slow build is
// always correct.
func planIncremental(ctx context.Context, src Source, o options, tools []string, goos, goarch, commit, goVersion string, clean bool) plan {
	whole := func(note string) plan { return plan{rebuild: tools, note: note} }
	switch {
	case commit == "":
		return whole("the checkout is not a git checkout")
	case !clean:
		return whole("the checkout has uncommitted changes")
	}
	platform := goos + "-" + goarch
	base, rec := findBase(o.out, o.version, platform, goVersion)
	if base == "" {
		return whole("no earlier build under --out recorded a clean commit, this Go and this stamp for " + platform)
	}
	baseDir := ArtifactDir(o.out, base, goos, goarch)
	sums, err := ReadSums(baseDir)
	if err == nil {
		_, err = VerifyArtifacts(baseDir, sums)
	}
	if err != nil {
		return whole("the base " + base + " does not verify: " + oneLine("", err))
	}
	changed, err := src.Changed(ctx, o.source, rec.Commit, commit)
	if err != nil {
		return whole("cannot diff " + rec.Commit + " against " + commit + ": " + oneLine("", err))
	}
	pkgs := make([]string, len(tools))
	for i, t := range tools {
		pkgs[i] = "./cmd/" + t
	}
	listed, err := src.Packages(ctx, o.source, goos, goarch, pkgs)
	if err != nil {
		return whole("go list cannot answer the imports: " + oneLine("", err))
	}
	dirs := map[string][]string{}
	for i, t := range tools {
		if d, ok := listed[pkgs[i]]; ok {
			dirs[t] = d
		}
	}
	held := map[string]bool{}
	for _, a := range sums {
		held[a.Name] = true
	}
	rebuild, reuse, _ := rebuildSet(tools, dirs, changed, func(t string) bool { return held[ToolFile(t, goos)] })
	return plan{base: base, baseDir: baseDir, sums: sums, rebuild: rebuild, reuse: reuse, changed: len(changed)}
}

// reuseFrom copies one tool's verified binary from the base into dir.
func reuseFrom(p plan, tool, goos, dir string) error {
	name := ToolFile(tool, goos)
	body, err := os.ReadFile(filepath.Join(p.baseDir, name))
	if err != nil {
		return err
	}
	for _, a := range p.sums {
		if a.Name == name && sumOf(body) != a.Sum {
			return fmt.Errorf("%s in %s changed after it was verified", name, p.baseDir)
		}
	}
	return writeNoFollow("reuse", filepath.Join(dir, name), body, 0o755)
}

func sumOf(body []byte) string {
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}

// ExecSource is the production Source: git and go in the checkout, each a
// read.
type ExecSource struct{}

func (ExecSource) Head(ctx context.Context, dir string) (string, bool, error) {
	commit, err := readChild(ctx, dir, nil, "git", "-C", dir, "rev-parse", "HEAD")
	if err != nil {
		return "", false, err
	}
	status, err := readChild(ctx, dir, nil, "git", "-C", dir, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(commit), strings.TrimSpace(status) == "", nil
}

func (ExecSource) Changed(ctx context.Context, dir, base, head string) ([]string, error) {
	out, err := readChild(ctx, dir, nil, "git", "-C", dir, "diff", "--name-only", "--no-renames", base, head)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// Packages is one `go list -deps` per platform for every tool at once: each
// package's import path, directory, standard flag and transitive imports.
func (ExecSource) Packages(ctx context.Context, dir, goos, goarch string, pkgs []string) (map[string][]string, error) {
	env := append(goenv.Clean(os.Environ()), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	args := append([]string{"list", "-deps", "-f", "{{.ImportPath}}\t{{.Dir}}\t{{.Standard}}\t{{join .Deps \" \"}}"}, pkgs...)
	out, err := readChild(ctx, dir, env, "go", args...)
	if err != nil {
		return nil, err
	}
	return parsePackages(out, dir, pkgs)
}

// parsePackages turns go list's lines into each asked package's directories
// relative to root. A package outside root (the module cache) is left out: it
// changes only through go.mod and go.sum, which rebuild everything. A tool's
// own line already carries the closure: .Deps is "all (recursively) imported
// dependencies" (`go help list`), not .Imports, so A -> B -> C puts C on A's
// line (TestATransitiveChangeRebuildsTheTool runs the real go list on that
// chain, and fails if .Imports is read instead).
func parsePackages(out, root string, pkgs []string) (map[string][]string, error) {
	real := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	abs = real(abs)
	dirOf := map[string]string{} // import path -> relative dir
	type line struct{ path, dir, deps string }
	var lines []line
	for _, l := range strings.Split(out, "\n") {
		f := strings.SplitN(l, "\t", 4)
		if len(f) != 4 || f[2] == "true" {
			continue
		}
		rel, err := filepath.Rel(abs, real(f[1]))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		dirOf[f[0]] = filepath.ToSlash(rel)
		lines = append(lines, line{f[0], filepath.ToSlash(rel), f[3]})
	}
	want := map[string]string{}
	for _, p := range pkgs {
		want[filepath.ToSlash(filepath.Clean(strings.TrimPrefix(p, "./")))] = p
	}
	answer := map[string][]string{}
	for _, l := range lines {
		pkg, ok := want[l.dir]
		if !ok {
			continue
		}
		dirs := []string{l.dir}
		for _, d := range strings.Fields(l.deps) {
			if rel, ok := dirOf[d]; ok {
				dirs = append(dirs, rel)
			}
		}
		sort.Strings(dirs)
		answer[pkg] = slices.Compact(dirs)
	}
	return answer, nil
}

func (ExecSource) GoVersion(ctx context.Context) (string, error) {
	out, err := readChild(ctx, "", goenv.Clean(os.Environ()), "go", "env", "GOVERSION")
	return strings.TrimSpace(out), err
}

// readChild runs one read and returns its stdout alone, bounded; stderr is
// kept for the error. go list prints a warning on stderr that is not part of
// its answer.
func readChild(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := bounded.NewCapture(localDiffCap, cancel)
	stderr := bounded.NewCapture(childCap, cancel)
	cmd := subproc.Context(runCtx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if stdout.Hit() {
		return "", fmt.Errorf("%s answered more than %d bytes", name, localDiffCap)
	}
	if err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(stderr.Bytes())))
	}
	return string(stdout.Bytes()), nil
}

// incrementalLine is what the build says about one platform's plan, on stdout
// above its RELEASE BUILT line.
func incrementalLine(w io.Writer, version, platform string, p plan) {
	if p.base == "" {
		fmt.Fprintf(w, "RELEASE BUILD WHOLE version=%s platform=%s rebuilt=%d reason=%s\n",
			field(version), field(platform), len(p.rebuild), field(p.note))
		return
	}
	rebuilt := strings.Join(p.rebuild, ",")
	fmt.Fprintf(w, "RELEASE BUILD INCREMENTAL version=%s platform=%s base=%s changed=%d rebuilt=%s reused=%d\n",
		field(version), field(platform), field(p.base), p.changed, field(rebuilt), len(p.reuse))
}
