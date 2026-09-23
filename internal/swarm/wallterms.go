package swarm

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
)

// THE HARNESS WALL (S7, nova-tools#2498). Johnny owns the terms; Rowan owns the
// launcher. This file is the terms: declared writes (PATHS) stay separate from
// dispatcher-approved contextual reads; the command list is a harness-prompt
// allowlist, not the OS wall; webfetch is deny; MODE: script is --net-deny. It
// does not rewrite nativeSandboxArgv or supervise's wrap — that is TODO
// launcher: Rowan. A half launcher that applied PATHS by --read of a parent
// directory would admit every sibling of a named file, which is not this rule.
//
// The OS wall still grants the job directory as --write (SPEC-SANDBOX the dispatcher
// caller). File-level scope is this matcher, not a recursive --read. A directory glob
// (`keep/**`) can be a --read root today; a file glob cannot. ReadRoots refuses a
// root that is unresolved or that follows a symlink out of the repository.

// ApprovedCommands is the closed harness-prompt allowlist. A first-token not in it
// is denied without prompting, so a card does not spend a turn on a permission
// refusal. This is not command confinement and not the OS wall: git, go and make
// can still invoke other programs. curl, wget, ssh, nova-sandbox, nova-secrets
// and a login shell are not in it. No new permissions.
var ApprovedCommands = []string{
	"cat", "diff", "git", "go", "gofmt", "grep", "head", "ls", "make", "rg", "wc",
}

// CardWallTerms is one card's S7 wall: declared write PATHS, contextual reads,
// the command allowlist, webfetch deny, and --net-deny when the header is MODE: script.
type CardWallTerms struct {
	Paths    []string // the card's PATHS: globs; empty when PATHS: none or absent
	Reads    []string // default contextual-read globs (not exhaustive): specs, siblings, testdata, TEST:
	Scope    []string // Paths union Reads; what AdmitsRead matches
	NetDeny  bool     // true for header MODE: script; a model job stays nopromise
	Webfetch string   // always FenceDeny
}

// WallTerms reads one card's S7 wall. An invalid PATHS: line is an error, the same
// error hygiene.ValidatePaths returns at cut. Duplicate or contradictory MODE
// fields refuse. MODE is the typed header's field, not a body line.
func WallTerms(card []byte) (CardWallTerms, error) {
	h, _ := cardHeaderBlock(card)
	script, err := headerScriptMode(h)
	if err != nil {
		return CardWallTerms{}, err
	}
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
	reads := ContextualReadGlobs(paths, h["TEST"].value)
	return CardWallTerms{
		Paths:    paths,
		Reads:    reads,
		Scope:    ScopeGlobs(paths, h["TEST"].value),
		NetDeny:  script,
		Webfetch: FenceDeny,
	}, nil
}

// headerScriptMode reports whether the typed header declares MODE: script.
// Duplicate or contradictory MODE fields refuse. A body-only MODE line is not
// in the header block and does not select script.
func headerScriptMode(h map[string]headerField) (bool, error) {
	f := h["MODE"]
	if f.again > 0 {
		return false, fmt.Errorf("MODE: is declared twice, on lines %d and %d; duplicate or contradictory MODE fields refuse", f.line, f.again)
	}
	if !f.found {
		return false, nil
	}
	return strings.EqualFold(f.value, "script"), nil
}

func cleanRepoRel(repoRel string) (string, bool) {
	p := path.Clean(strings.ReplaceAll(strings.TrimSpace(repoRel), "\\", "/"))
	if p == "." || p == "/" || strings.HasPrefix(p, "../") {
		return "", false
	}
	return strings.TrimPrefix(p, "./"), true
}

func admitsGlob(globs []string, repoRel string) bool {
	p, ok := cleanRepoRel(repoRel)
	if !ok {
		return false
	}
	for _, g := range globs {
		if hygiene.MatchGlob(g, p) {
			return true
		}
	}
	return false
}

// AdmitsRead reports whether a repo-relative path matches the declared writes
// or the default contextual reads (specs, siblings, testdata, TEST:). Those
// defaults are not exhaustive: the dispatcher may authorize bounded
// caller/callee, build-input, and reverse-dependent reads without widening
// PATHS. A denied required read blocks and requests that adjustment; this
// helper does not guess. Dispatcher reads
// (slot, toolchain, read_roots) are not this question.
func (t CardWallTerms) AdmitsRead(repoRel string) bool {
	return admitsGlob(t.Scope, repoRel)
}

// AdmitsWrite reports whether a repo-relative path matches a declared PATHS glob.
// Contextual reads are not writes.
func (t CardWallTerms) AdmitsWrite(repoRel string) bool {
	return admitsGlob(t.Paths, repoRel)
}

// AdmitsCommand reports whether a harness bash line is on the harness-prompt
// allowlist. It is not command confinement and not the OS wall: compound lines
// (`;`, `&&`, `||`, `|`, backtick, `$()`, newline) are refused, and a first
// token containing `/` is refused, so `git status; curl` and `/usr/bin/git`
// do not pass as `git`. Background `&` and process substitutions further show
// this is not a shell parser; the hint is explicitly non-authoritative.
func AdmitsCommand(line string) bool {
	if commandLineIsCompound(line) {
		return false
	}
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return false
	}
	tok := strings.Trim(fields[0], `"'`)
	if tok == "" || strings.Contains(filepath.ToSlash(tok), "/") {
		return false
	}
	for _, c := range ApprovedCommands {
		if tok == c {
			return true
		}
	}
	return false
}

func commandLineIsCompound(line string) bool {
	if strings.ContainsAny(line, ";|`\n\r") {
		return true
	}
	return strings.Contains(line, "&&") || strings.Contains(line, "$(")
}

// AdmitsFetch reports whether a webfetch is in the wall. It is never in the
// wall: S7 is no web. The argument is the URL a test names.
func AdmitsFetch(string) bool { return false }

// ReadRoots is the --read directories a directory-covering PATHS glob can name
// today, under repoRoot, skip-if-absent. A file glob names none: granting its
// parent would admit siblings outside PATHS. A root that cannot be resolved, or
// that follows a symlink out of repoRoot, is refused before a policy is
// produced. TODO launcher: Rowan wires this into nativeSandboxArgv for those
// directory globs; file-level scope stays the matcher above.
func (t CardWallTerms) ReadRoots(repoRoot string) ([]string, error) {
	if strings.TrimSpace(repoRoot) == "" {
		return nil, nil
	}
	repoAbs, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("read root: repo %q unresolved: %w", repoRoot, err)
	}
	repoReal, err := filepath.EvalSymlinks(repoAbs)
	if err != nil {
		return nil, fmt.Errorf("read root: repo %q unresolved: %w", repoRoot, err)
	}
	var out []string
	seen := map[string]bool{}
	for _, g := range t.Paths {
		dir, ok := wholeDirGlob(g)
		if !ok {
			continue
		}
		abs := filepath.Join(repoAbs, filepath.FromSlash(dir))
		if _, err := os.Lstat(abs); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read root %q: %w", dir, err)
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, fmt.Errorf("read root %q is unresolved", dir)
		}
		fi, err := os.Stat(real)
		if err != nil {
			return nil, fmt.Errorf("read root %q is unresolved", dir)
		}
		if !fi.IsDir() {
			continue
		}
		if !staysInRepo(real, repoReal) {
			return nil, fmt.Errorf("read root %q escapes the repository", dir)
		}
		if seen[real] {
			continue
		}
		seen[real] = true
		out = append(out, real)
	}
	return out, nil
}

func staysInRepo(abs, repoReal string) bool {
	rel, err := filepath.Rel(repoReal, abs)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel != ".." && !strings.HasPrefix(rel, "../")
}

// ScopeGlobs is declared writes plus contextual reads: PATHS, docs/SPEC-*.md,
// same-package siblings, testdata under those directories, and
// `<package>/*_test.go` from a `TEST:` line.
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
	}
	for _, g := range ContextualReadGlobs(paths, testLine) {
		add(g)
	}
	return out
}

// ContextualReadGlobs is the dispatcher-approved read set that is not a write:
// `docs/SPEC-*.md`, same-package siblings of a PATHS glob, `testdata/` under the
// same directory, and the TEST: package's `*_test.go` files.
func ContextualReadGlobs(paths []string, testLine string) []string {
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
	add("docs/SPEC-*.md")
	for _, p := range paths {
		add(packageSiblingGlob(p))
		add(testdataGlob(p))
	}
	for _, g := range testPackageGlobs(testLine) {
		add(g)
	}
	return out
}

func packageSiblingGlob(glob string) string {
	dir := literalDir(glob)
	if dir == "" || dir == "." {
		return ""
	}
	return dir + "/*"
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
