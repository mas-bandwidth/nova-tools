// Command nova-update installs nova-tools releases on this machine: it compares the
// installed binaries with a GitHub release, and installs one tool or all of them, every
// byte checked against the release's SHA256SUMS before anything is replaced.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// version is stamped at build time: -ldflags "-X main.version=<tag>".
var version string

const usage = `nova-update: install nova-tools releases on this machine.

  nova-update [report] [--bin <dir>] [--version <v>] [--repo <owner/name>]
      Compare each nova-* binary in the bin directory with the release (default: the
      latest), by checksum: one TOOL line each (current, stale, missing, unreleased),
      then one REPORT line. Exit 0 when every released tool installed is current.
  nova-update install [<tool> | all] [--bin <dir>] [--version <v>] [--repo <owner/name>] [--dry-run]
      Install one tool, or every tool the release ships for this platform (all, the
      default), from the release. Every download is checked against SHA256SUMS before
      the first binary is replaced; a tool already holding the release's bytes is skipped.
  nova-update release install --from <dir> --version <v> --bin <dir> [--retire <dir>] [--platform <goos-goarch>]
      Install a release that was built on disk, <from>/<v>/<goos-goarch>/ with its
      SHA256SUMS, the same way; --retire removes the release's tools from a second
      directory. Prints RELEASE INSTALLED version=<v> tools=<n> skipped=<n> ...
  nova-update version

The bin directory defaults to the one this nova-update lives in; the repository to
mas-bandwidth/nova-tools. GITHUB_TOKEN or GH_TOKEN, when set, is sent to the GitHub API
(never printed). status is report, apply is install. Exit 2 is a refusal: nothing changed.
`

const defaultRepo = "mas-bandwidth/nova-tools"

// sumsFile is the checksum file of a release, in the format sha256sum -c reads.
const sumsFile = "SHA256SUMS"

// Artifact is one file a release ships: its name and sha256.
type Artifact struct{ Name, Sum string }

// Release is what nova-update needs of a GitHub release.
type Release interface {
	// Tag is the release's tag; Sums its SHA256SUMS file.
	Tag() string
	Sums(ctx context.Context) ([]byte, error)
	// Open streams one asset.
	Open(ctx context.Context, asset string) (io.ReadCloser, error)
}

// Forge finds a release by tag, or the latest when the tag is "".
type Forge func(ctx context.Context, repo, tag string) (Release, error)

type env struct {
	stdout, stderr io.Writer
	forge          Forge
	goos, goarch   string
	exe            func() (string, error)
}

func main() {
	e := env{stdout: os.Stdout, stderr: os.Stderr, forge: githubForge(os.Getenv), goos: runtime.GOOS, goarch: runtime.GOARCH, exe: os.Executable}
	os.Exit(run(context.Background(), os.Args[1:], e))
}

func run(ctx context.Context, args []string, e env) int {
	verb := "report"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb, args = args[0], args[1:]
	}
	switch verb {
	case "help", "-h", "--help":
		fmt.Fprint(e.stdout, usage)
		return 0
	case "version", "--version":
		fmt.Fprintf(e.stdout, "nova-update %s %s/%s %s\n", stamp(), runtime.GOOS, runtime.GOARCH, runtime.Version())
		return 0
	case "report", "status":
		return report(ctx, args, e)
	case "install", "apply":
		return installVerb(ctx, args, e)
	case "release":
		if len(args) == 0 || args[0] != "install" {
			return refuse(e, "RELEASE", "the only release verb here is install; cutting and building a release is not this tool's")
		}
		return releaseInstall(ctx, args[1:], e)
	}
	return refuse(e, "UPDATE", "unknown verb %q (report, install, release install, version, help)", verb)
}

func stamp() string {
	if version == "" {
		return "devel"
	}
	return version
}

func refuse(e env, token, format string, a ...any) int {
	fmt.Fprintf(e.stderr, "%s REFUSED: %s; run: nova-update help\n", token, fmt.Sprintf(format, a...))
	return 2
}

// common is the flags report and install share.
type common struct {
	bin, tag, repo string
	dryRun         bool
}

func parse(name string, args []string, e env, c *common, withDry bool) ([]string, error) {
	fs := flag.NewFlagSet("nova-update "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&c.bin, "bin", "", "")
	fs.StringVar(&c.tag, "version", "", "")
	fs.StringVar(&c.repo, "repo", defaultRepo, "")
	if withDry {
		fs.BoolVar(&c.dryRun, "dry-run", false, "")
	}
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if c.bin == "" {
		exe, err := e.exe()
		if err == nil {
			exe, err = filepath.EvalSymlinks(exe)
		}
		if err != nil {
			return nil, fmt.Errorf("cannot find this binary's directory (%v): give --bin <dir>", err)
		}
		c.bin = filepath.Dir(exe)
	}
	return fs.Args(), nil
}

// suffix is the end of a release asset's name for this platform: <tool>_<tag>_<goos>_<goarch>[.exe].
func (e env) suffix(tag string) string {
	return "_" + tag + "_" + e.goos + "_" + e.goarch + e.exeSuffix()
}

func (e env) exeSuffix() string {
	if e.goos == "windows" {
		return ".exe"
	}
	return ""
}

// released maps each tool the release ships for this platform to its asset.
func released(e env, tag string, sums []Artifact) map[string]Artifact {
	out := map[string]Artifact{}
	sfx := e.suffix(tag)
	for _, a := range sums {
		if tool, ok := strings.CutSuffix(a.Name, sfx); ok && strings.HasPrefix(tool, "nova-") {
			out[tool] = a
		}
	}
	return out
}

func fetch(ctx context.Context, e env, c common) (Release, map[string]Artifact, error) {
	rel, err := e.forge(ctx, c.repo, c.tag)
	if err != nil {
		return nil, nil, err
	}
	raw, err := rel.Sums(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot read %s of %s: %w", sumsFile, rel.Tag(), err)
	}
	sums, err := parseSums(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("%s of %s: %w", sumsFile, rel.Tag(), err)
	}
	tools := released(e, rel.Tag(), sums)
	if len(tools) == 0 {
		return nil, nil, fmt.Errorf("%s ships no tool for %s-%s", rel.Tag(), e.goos, e.goarch)
	}
	return rel, tools, nil
}

func report(ctx context.Context, args []string, e env) int {
	var c common
	rest, err := parse("report", args, e, &c, false)
	if err != nil {
		return refuse(e, "REPORT", "%v", err)
	}
	if len(rest) != 0 {
		return refuse(e, "REPORT", "report takes no arguments, got %q", rest[0])
	}
	rel, tools, err := fetch(ctx, e, c)
	if err != nil {
		return refuse(e, "REPORT", "%v", err)
	}
	installed, err := installedTools(c.bin, e.exeSuffix())
	if err != nil {
		return refuse(e, "REPORT", "cannot read %s: %v", c.bin, err)
	}
	names := map[string]bool{}
	for t := range tools {
		names[t] = true
	}
	for t := range installed {
		names[t] = true
	}
	counts := map[string]int{}
	for _, t := range sortedKeys(names) {
		a, inRelease := tools[t]
		state := "unreleased"
		have := "none"
		if _, ok := installed[t]; ok {
			sum, err := fileSum(filepath.Join(c.bin, t+e.exeSuffix()))
			if err != nil {
				return refuse(e, "REPORT", "cannot read %s: %v", t, err)
			}
			have = sum[:12]
			if inRelease {
				state = map[bool]string{true: "current", false: "stale"}[sum == a.Sum]
			}
		} else {
			state = "missing"
		}
		counts[state]++
		fmt.Fprintf(e.stdout, "TOOL name=%s state=%s installed=%s\n", t, state, have)
	}
	fmt.Fprintf(e.stdout, "REPORT version=%s platform=%s-%s bin=%s current=%d stale=%d missing=%d unreleased=%d\n",
		rel.Tag(), e.goos, e.goarch, c.bin, counts["current"], counts["stale"], counts["missing"], counts["unreleased"])
	if counts["stale"] > 0 {
		return 1
	}
	return 0
}

func installVerb(ctx context.Context, args []string, e env) int {
	var c common
	var tool []string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		tool, args = args[:1], args[1:]
	}
	rest, err := parse("install", args, e, &c, true)
	if err != nil {
		return refuse(e, "INSTALL", "%v", err)
	}
	rest = append(tool, rest...)
	if len(rest) > 1 {
		return refuse(e, "INSTALL", "install takes one tool or all, got %q", strings.Join(rest, " "))
	}
	rel, tools, err := fetch(ctx, e, c)
	if err != nil {
		return refuse(e, "INSTALL", "%v", err)
	}
	var items []item
	for _, t := range sortedKeys(tools) {
		if len(rest) == 1 && rest[0] != "all" && rest[0] != t {
			continue
		}
		asset := tools[t].Name
		items = append(items, item{name: t + e.exeSuffix(), sum: tools[t].Sum, open: func() (io.ReadCloser, error) { return rel.Open(ctx, asset) }})
	}
	if len(items) == 0 {
		return refuse(e, "INSTALL", "%s ships no %s for %s-%s", rel.Tag(), rest[0], e.goos, e.goarch)
	}
	if c.dryRun {
		for _, it := range items {
			action := "install"
			if sum, err := fileSum(filepath.Join(c.bin, it.name)); err == nil && sum == it.sum {
				action = "skip"
			}
			fmt.Fprintf(e.stdout, "WOULD %s %s\n", strings.ToUpper(action), it.name)
		}
		return 0
	}
	n, skipped, err := place(c.bin, items, e.stderr)
	if err != nil {
		fmt.Fprintf(e.stderr, "INSTALL FAILED version=%s bin=%s: %v (nothing was left half-replaced; install again)\n", rel.Tag(), c.bin, err)
		return 1
	}
	fmt.Fprintf(e.stdout, "INSTALLED version=%s tools=%d skipped=%d bin=%s platform=%s-%s\n", rel.Tag(), n, skipped, c.bin, e.goos, e.goarch)
	return 0
}

func releaseInstall(ctx context.Context, args []string, e env) int {
	fs := flag.NewFlagSet("nova-update release install", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	from := fs.String("from", "", "")
	tag := fs.String("version", "", "")
	bin := fs.String("bin", "", "")
	retire := fs.String("retire", "", "")
	platform := fs.String("platform", "", "")
	timeout := fs.Duration("timeout", 10*time.Minute, "")
	if err := fs.Parse(args); err != nil {
		return refuse(e, "INSTALL", "%v", err)
	}
	if *from == "" || *tag == "" || *bin == "" || fs.NArg() != 0 {
		return refuse(e, "INSTALL", "give --from <dir> --version <v> --bin <dir>, and nothing else")
	}
	goos, goarch := e.goos, e.goarch
	if *platform != "" {
		var ok bool
		if goos, goarch, ok = strings.Cut(*platform, "-"); !ok || goos == "" || goarch == "" {
			return refuse(e, "INSTALL", "--platform %q is not <goos>-<goarch>", *platform)
		}
	}
	dir := filepath.Join(*from, *tag, goos+"-"+goarch)
	raw, err := os.ReadFile(filepath.Join(dir, sumsFile))
	if err != nil {
		return refuse(e, "INSTALL", "there is no %s for %s-%s at %s: %v", *tag, goos, goarch, dir, err)
	}
	sums, err := parseSums(raw)
	if err != nil {
		return refuse(e, "INSTALL", "%s: %v", filepath.Join(dir, sumsFile), err)
	}
	if *retire != "" {
		if a, b := absClean(*retire), absClean(*bin); a == b {
			return refuse(e, "INSTALL", "--retire is --bin: the release would be removed as it is installed")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	items := make([]item, len(sums))
	for i, a := range sums {
		p := filepath.Join(dir, a.Name)
		items[i] = item{name: a.Name, sum: a.Sum, open: func() (io.ReadCloser, error) { return os.Open(p) }}
	}
	if err := ctx.Err(); err != nil {
		return refuse(e, "INSTALL", "%v", err)
	}
	n, skipped, err := place(*bin, items, e.stderr)
	if err != nil {
		fmt.Fprintf(e.stderr, "INSTALL FAILED version=%s bin=%s: %v (nothing was left half-replaced; install again)\n", *tag, *bin, err)
		return 1
	}
	retired := 0
	if *retire != "" {
		for _, a := range sums {
			p := filepath.Join(*retire, a.Name)
			if info, err := os.Lstat(p); err == nil && info.Mode().IsRegular() && strings.HasPrefix(a.Name, "nova-") {
				if err := os.Remove(p); err != nil {
					fmt.Fprintf(e.stderr, "INSTALL FAILED version=%s: installed, but cannot retire %s: %v\n", *tag, p, err)
					return 1
				}
				retired++
			}
		}
	}
	pruned, failed := prune(*from, *tag)
	fmt.Fprintf(e.stdout, "RELEASE INSTALLED version=%s tools=%d skipped=%d retired=%d bin=%s platform=%s-%s pruned=%d prune-failed=%d\n",
		*tag, n, skipped, retired, *bin, goos, goarch, pruned, failed)
	return 0
}

func absClean(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return filepath.Clean(p)
}

// parseSums reads sha256sum lines; a name with a path in it is refused.
func parseSums(raw []byte) ([]Artifact, error) {
	var out []Artifact
	for i, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		sum, name, ok := strings.Cut(strings.TrimSpace(line), "  ")
		name = strings.TrimPrefix(name, "*")
		if !ok || len(sum) != 64 || name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			return nil, fmt.Errorf("line %d is not <sha256>  <file>: %q", i+1, line)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return nil, fmt.Errorf("line %d: %q is not a sha256", i+1, sum)
		}
		out = append(out, Artifact{Name: name, Sum: strings.ToLower(sum)})
	}
	if len(out) == 0 {
		return nil, errors.New("it lists no file")
	}
	return out, nil
}

func installedTools(bin, exe string) (map[string]bool, error) {
	entries, err := os.ReadDir(bin)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, d := range entries {
		name, ok := strings.CutSuffix(d.Name(), exe)
		if ok && strings.HasPrefix(name, "nova-") && !strings.ContainsAny(name, "._") && d.Type().IsRegular() {
			out[name] = true
		}
	}
	return out, nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }() // ignored: opened for reading
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
