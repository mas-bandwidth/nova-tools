package swarm

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
)

// THE HARNESS WALL (S7, nova-tools#2498). Johnny owns the terms; Rowan owns the
// launcher. This file is the terms: what one card may read from the repository, which
// commands the harness may run without prompting, that webfetch is deny, and that
// MODE: script is --net-deny. It does not rewrite nativeSandboxArgv or supervise's wrap
// — that is TODO launcher: Rowan. A half launcher that applied PATHS by --read of a
// parent directory would admit every sibling of a named file, which is not this rule.
//
// The OS wall still grants the job directory as --write (SPEC-SANDBOX the dispatcher
// caller). File-level scope is this matcher, not a recursive --read. A directory glob
// (`keep/**`) can be a --read root today; a file glob cannot.

// ApprovedCommands is the closed harness command set. A first-token not in it is
// denied without prompting, so a card does not spend a turn on a permission refusal.
// curl, wget, ssh, nova-sandbox, nova-secrets and a login shell are not in it.
var ApprovedCommands = []string{
	"cat", "diff", "git", "go", "gofmt", "grep", "head", "ls", "make", "rg", "wc",
}

// CardWallTerms is one card's S7 wall: PATHS plus the tests of those paths, the
// command allowlist, webfetch deny, and --net-deny when the card is MODE: script.
type CardWallTerms struct {
	Paths    []string // the card's PATHS: globs; empty when PATHS: none or absent
	Scope    []string // PATHS plus the tests of those paths
	NetDeny  bool     // true for MODE: script; a model job stays nopromise
	Webfetch string   // always FenceDeny
}

// WallTerms reads one card's S7 wall. An invalid PATHS: line is an error, the same
// error hygiene.ValidatePaths returns at cut.
func WallTerms(card []byte) (CardWallTerms, error) {
	h, _ := cardHeaderBlock(card)
	var paths []string
	switch pv := h["PATHS"].value; {
	case pv == "" || pv == "none":
	default:
		for _, g := range strings.Split(pv, ",") {
			g = strings.TrimSpace(g)
			if g == "" {
				continue
			}
			paths = append(paths, g)
		}
		if err := hygiene.ValidatePaths(paths); err != nil {
			return CardWallTerms{}, err
		}
	}
	return CardWallTerms{
		Paths:    paths,
		Scope:    ScopeGlobs(paths, h["TEST"].value),
		NetDeny:  cardScriptMode(string(card)),
		Webfetch: FenceDeny,
	}, nil
}

// cardScriptMode reports whether the card's own lines declare `MODE: script`.
func cardScriptMode(raw string) bool {
	for _, ln := range strings.Split(raw, "\n") {
		if strings.EqualFold(strings.TrimSpace(ln), "MODE: script") {
			return true
		}
	}
	return false
}

// AdmitsRead reports whether a repo-relative path is in the card-scope read set.
// Dispatcher reads (slot, toolchain, read_roots) are not this question.
func (t CardWallTerms) AdmitsRead(repoRel string) bool {
	p := path.Clean(strings.ReplaceAll(strings.TrimSpace(repoRel), "\\", "/"))
	if p == "." || p == "/" || strings.HasPrefix(p, "../") {
		return false
	}
	p = strings.TrimPrefix(p, "./")
	for _, g := range t.Scope {
		if hygiene.MatchGlob(g, p) {
			return true
		}
	}
	return false
}

// AdmitsCommand reports whether a harness bash line's first token is in the
// pre-approved set. The allowlist is the command name, not a shell grammar.
func AdmitsCommand(line string) bool {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return false
	}
	base := path.Base(filepath.ToSlash(strings.Trim(fields[0], `"'`)))
	for _, c := range ApprovedCommands {
		if base == c {
			return true
		}
	}
	return false
}

// AdmitsFetch reports whether a webfetch is in the wall. It is never in the
// wall: S7 is no web. The argument is the URL a test names.
func AdmitsFetch(string) bool { return false }

// ReadRoots is the --read directories a directory-covering PATHS glob can name
// today, under repoRoot, skip-if-absent. A file glob names none: granting its
// parent would admit siblings outside PATHS. TODO launcher: Rowan wires this
// into nativeSandboxArgv for those directory globs; file-level scope stays the
// matcher above.
func (t CardWallTerms) ReadRoots(repoRoot string) []string {
	if strings.TrimSpace(repoRoot) == "" {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, g := range t.Scope {
		dir, ok := wholeDirGlob(g)
		if !ok {
			continue
		}
		abs := filepath.Join(repoRoot, filepath.FromSlash(dir))
		fi, err := os.Stat(abs)
		if err != nil || !fi.IsDir() {
			continue
		}
		if got, err := filepath.EvalSymlinks(abs); err == nil {
			abs = got
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	return out
}

// ScopeGlobs is PATHS plus the tests of those paths: the `_test.go` sibling of a
// named `.go` file, `testdata/` under the same directory, and `<package>/*_test.go`
// from a `TEST: <package> <TestName>` line.
func ScopeGlobs(paths []string, testLine string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(g string) {
		g = strings.TrimSpace(g)
		if g == "" || seen[g] {
			return
		}
		seen[g] = true
		out = append(out, g)
	}
	for _, p := range paths {
		add(p)
		add(goTestSibling(p))
		add(testdataGlob(p))
	}
	for _, g := range testPackageGlobs(testLine) {
		add(g)
	}
	return out
}

func goTestSibling(glob string) string {
	base := path.Base(glob)
	if strings.ContainsAny(base, "*?") {
		return ""
	}
	if !strings.HasSuffix(base, ".go") || strings.HasSuffix(base, "_test.go") {
		return ""
	}
	dir := path.Dir(glob)
	name := strings.TrimSuffix(base, ".go") + "_test.go"
	if dir == "." {
		return name
	}
	return dir + "/" + name
}

func testdataGlob(glob string) string {
	dir := literalDir(glob)
	if dir == "" || dir == "." {
		return ""
	}
	return dir + "/testdata/**"
}

func literalDir(glob string) string {
	var lit []string
	for _, s := range strings.Split(glob, "/") {
		if strings.ContainsAny(s, "*?") {
			break
		}
		lit = append(lit, s)
	}
	if len(lit) == 0 {
		return ""
	}
	last := lit[len(lit)-1]
	if strings.Contains(last, ".") && !strings.HasPrefix(last, ".") {
		lit = lit[:len(lit)-1]
	}
	if len(lit) == 0 {
		return ""
	}
	return strings.Join(lit, "/")
}

func testPackageGlobs(testLine string) []string {
	testLine = strings.TrimSpace(testLine)
	if testLine == "" || testLine == "none" {
		return nil
	}
	fields := strings.Fields(testLine)
	if len(fields) == 0 {
		return nil
	}
	pkg := strings.TrimPrefix(fields[0], "./")
	pkg = strings.TrimSuffix(pkg, "/")
	if pkg == "" || strings.Contains(pkg, "..") {
		return nil
	}
	return []string{pkg + "/*_test.go"}
}

func wholeDirGlob(glob string) (string, bool) {
	glob = strings.TrimSuffix(strings.TrimSpace(glob), "/")
	if glob == "" || strings.Contains(glob, "..") {
		return "", false
	}
	if strings.HasSuffix(glob, "/**") {
		dir := strings.TrimSuffix(glob, "/**")
		if dir == "" || strings.ContainsAny(dir, "*?") {
			return "", false
		}
		return dir, true
	}
	if strings.ContainsAny(glob, "*?") {
		return "", false
	}
	if strings.Contains(path.Base(glob), ".") && !strings.HasPrefix(path.Base(glob), ".") {
		return "", false
	}
	return glob, true
}
