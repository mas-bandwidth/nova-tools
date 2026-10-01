package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// discardedAllowlistPath is the shrink-only ledger of the discarded errors the
// living Go still carries without a reason: one `file:function:shape` per row,
// a reason after it. A site leaves the ledger by gaining its `// ignored:`
// comment, by being fixed, or by going away; it never enters it again.
const discardedAllowlistPath = "testdata/discarded"

// discardedDirs are the directories of the living, non-test Go this rule reads.
// deprecated/ is never walked (the shared tree skips it) and testdata/ holds
// fixtures, not code that runs.
var discardedDirs = []string{"cmd", "internal", "tools"}

// ignoredMarker is the house form of a reason: `// ignored: <reason>` on the
// line of the discard or on the line above it.
const ignoredMarker = "// ignored:"

// The three shapes the rule reads.
const (
	shapeBlank     = "blank"       // `_ = f()`, `_, _ = f()`, `_ = err`
	shapeBareRet   = "bare-return" // `if err != nil { return }`, err unread
	shapeErrNilled = "err-nil"     // `err = nil`
)

// TestNoErrorIsDiscarded is the owner's rule (2026-09-30 ~19:20 ET): every tool
// and verb "should never fail silently, and they should always provide helpful
// breadcrumbs how to fix anything going wrong". An error thrown away in the Go
// is the first place a failure goes silent, so in every non-test .go file under
// cmd/, internal/ and tools/ each of three shapes carries its reason where it
// stands:
//
//   - `_ = <call>` and `_, _ = <call>` (every result blank), and `_ = err`: the
//     call's error is dropped on the spot;
//   - `if err != nil { ... return }` with a bare return and no read of err in
//     the body: the function ends early and says nothing;
//   - `err = nil`: a failure is overwritten.
//
// The reason is a comment in the house form, `// ignored: <reason>`, on the same
// line or the line above ("a best-effort cleanup; the write's error is the one
// returned", "a close after a read that already succeeded"). A site with no
// reason is a red run unless its `file:function:shape` is in the ledger, and a
// ledger row naming no such site is a red run too, so the ledger only shrinks.
func TestNoErrorIsDiscarded(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	ledger := newSiteLedger(t, discardedAllowlistPath)
	for _, src := range tree.GoFilesUnder(false, discardedDirs...) {
		if src.HasDirNamed("testdata") {
			continue
		}
		if src.ParseErr != nil {
			t.Fatal(src.ParseErr)
		}
		lines := strings.Split(string(src.Src), "\n")
		for _, s := range discardedSites(tree.FSet, src.AST) {
			if reasonedAt(lines, s.line) {
				continue
			}
			ledger.add(src.Rel+":"+s.fn+":"+s.shape, fmt.Sprintf("%s:%d (%s)", src.Rel, s.line, s.text))
		}
	}
	for _, v := range ledger.violations(t, discardedRemedy) {
		t.Error(v)
	}
}

// discardedRemedy is what a finding says to do: fix the site or give it its reason. A
// row in the ledger is how an old site is carried, never how a new one is admitted.
const discardedRemedy = "the error is dropped, or the function ends early, or a failure is overwritten; return it, print it as one line with its remedy, or say why it is safe with `// ignored: <reason>` on this line or the one above (a row's count only falls)"

// discardedSite is one place the rule reads.
type discardedSite struct {
	fn, shape, text string
	line            int
}

// discardedSites walks every function of f (a function literal belongs to the
// declaration it stands in) and returns the three shapes.
func discardedSites(fset *token.FileSet, f *ast.File) []discardedSite {
	var out []discardedSite
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		name := removeAllFuncName(fn)
		add := func(n ast.Node, shape, text string) {
			out = append(out, discardedSite{fn: name, shape: shape, text: text, line: fset.Position(n.Pos()).Line})
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignStmt:
				if blankDiscard(x) {
					add(x, shapeBlank, exprText(x.Rhs[0]))
				}
				if errNilled(x) {
					add(x, shapeErrNilled, exprText(x.Lhs[0])+" = nil")
				}
			case *ast.IfStmt:
				if id := errNotNil(x.Cond); id != "" && bareReturnIgnoring(x.Body, id) {
					add(x, shapeBareRet, "if "+id+" != nil { return }")
				}
			}
			return true
		})
	}
	return out
}

// blankDiscard reports `_ = <call>`, `_, _ = <call>` (every left side blank) and
// `_ = <error-named identifier>`.
func blankDiscard(a *ast.AssignStmt) bool {
	if a.Tok != token.ASSIGN || len(a.Rhs) != 1 {
		return false
	}
	for _, l := range a.Lhs {
		if id, ok := l.(*ast.Ident); !ok || id.Name != "_" {
			return false
		}
	}
	switch r := a.Rhs[0].(type) {
	case *ast.CallExpr:
		return !conversionOrBuiltin(r) && !flagDefinition(r)
	case *ast.Ident:
		return len(a.Lhs) == 1 && silentErrIdent.MatchString(r.Name)
	}
	return false
}

// conversionOrBuiltin reports a call that cannot return an error: a builtin
// (append, len, copy...) or a conversion spelt with a basic type.
func conversionOrBuiltin(c *ast.CallExpr) bool {
	id, ok := c.Fun.(*ast.Ident)
	if !ok {
		return false
	}
	switch id.Name {
	case "append", "len", "cap", "copy", "new", "make", "min", "max",
		"string", "int", "int64", "uint64", "byte", "rune", "float64":
		return true
	}
	return false
}

// flagDefinitions are the flag.FlagSet methods that define a flag and return
// its pointer, not an error: `_ = fs.String("owner", "", "")` accepts a flag
// nobody reads, and drops nothing.
var flagDefinitions = map[string]bool{
	"String": true, "Int": true, "Bool": true, "Int64": true, "Uint": true,
	"Uint64": true, "Float64": true, "Duration": true,
}

// flagDefinition reports a call spelt like a flag definition: a method from
// flagDefinitions with the three arguments (name, value, usage).
func flagDefinition(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	return ok && flagDefinitions[sel.Sel.Name] && len(c.Args) == 3
}

// errNilled reports `err = nil` for an error-named identifier.
func errNilled(a *ast.AssignStmt) bool {
	if a.Tok != token.ASSIGN || len(a.Lhs) != 1 || len(a.Rhs) != 1 {
		return false
	}
	l, ok := a.Lhs[0].(*ast.Ident)
	if !ok || !silentErrIdent.MatchString(l.Name) {
		return false
	}
	r, ok := a.Rhs[0].(*ast.Ident)
	return ok && r.Name == "nil"
}

// errNotNil returns the identifier of an `<err> != nil` condition, or "".
func errNotNil(cond ast.Expr) string {
	b, ok := cond.(*ast.BinaryExpr)
	if !ok || b.Op != token.NEQ {
		return ""
	}
	id, ok := b.X.(*ast.Ident)
	if !ok || !silentErrIdent.MatchString(id.Name) {
		return ""
	}
	if r, ok := b.Y.(*ast.Ident); !ok || r.Name != "nil" {
		return ""
	}
	return id.Name
}

// bareReturnIgnoring reports a block that ends in a `return` with no results
// and never reads the error named id: the early exit says nothing.
func bareReturnIgnoring(body *ast.BlockStmt, id string) bool {
	if len(body.List) == 0 {
		return false
	}
	ret, ok := body.List[len(body.List)-1].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 0 {
		return false
	}
	read := false
	ast.Inspect(body, func(n ast.Node) bool {
		if x, ok := n.(*ast.Ident); ok && x.Name == id {
			read = true
		}
		return !read
	})
	return !read
}

// reasonedAt reports whether line (1-based) or the line above it carries the
// house reason, `// ignored: <reason>` with a reason after the colon.
func reasonedAt(lines []string, line int) bool {
	for _, i := range []int{line - 1, line - 2} {
		if i < 0 || i >= len(lines) {
			continue
		}
		if _, after, ok := strings.Cut(lines[i], ignoredMarker); ok && strings.TrimSpace(after) != "" {
			return true
		}
	}
	return false
}

// exprText is a short spelling of e for a finding line.
func exprText(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return exprText(x.X) + "." + x.Sel.Name
	case *ast.CallExpr:
		return exprText(x.Fun) + "(...)"
	case *ast.StarExpr:
		return "*" + exprText(x.X)
	case *ast.IndexExpr:
		return exprText(x.X) + "[...]"
	case *ast.ParenExpr:
		return "(" + exprText(x.X) + ")"
	}
	return "<expr>"
}

// countedLedger is the options of the four never-silent ledgers (discarded,
// scripthide, okonfailure, remedy): shrink-only by site, not by row. A row is
// `key sites reason`, and the measured number of sites under the key must equal
// its count, so a new site under an existing row is red, and a site that has been
// fixed asks for its count to be lowered (NOVA_CI_UPDATE=1 lowers it).
var countedLedger = allowlist.Options{Ceiling: true, Counted: true}

// missingReasons names counted rows that carry no reason after their site
// count: a row with no reason is a parking place, not a judgement.
func missingReasons(allow *allowlist.Packages) []string {
	var missing []string
	for _, list := range allow.Lists() {
		for _, row := range list.Rows() {
			if f := strings.Fields(row.Text); len(f) < 3 {
				missing = append(missing, fmt.Sprintf("%s:%d: %q carries no reason after its site count; a row says why the sites are left as they are", list.Path, row.Line, row.Text))
			}
		}
	}
	return missing
}

// requireReasons stops this class test before any ledger update when a shard
// contains a reasonless row.
func requireReasons(t *testing.T, allow *allowlist.Packages) {
	t.Helper()
	missing := missingReasons(allow)
	for _, problem := range missing {
		t.Error(problem)
	}
	if len(missing) > 0 {
		t.FailNow()
	}
}

// siteLedger is one class test's run against its counted ledger: the sites it
// measured, each under its key, and what the ledger makes of them.
type siteLedger struct {
	path  string
	allow *allowlist.Packages
	sites map[string][]string // key -> "file:line: text", in the order found
}

func newSiteLedger(t *testing.T, path string) *siteLedger {
	t.Helper()
	allow, err := allowlist.LoadPackages(path, countedLedger)
	if err != nil {
		t.Fatal(err)
	}
	requireReasons(t, allow)
	return &siteLedger{path: path, allow: allow, sites: map[string][]string{}}
}

// add records one unremedied site under its key.
func (l *siteLedger) add(key, where string) { l.sites[key] = append(l.sites[key], where) }

// violations checks the measured sites against the ledger: a key with no row is red
// at each of its sites (remedy says how to fix one); a key with more sites than its
// row lists is red with every site named, because the new one cannot be told from the
// listed ones; a key with fewer sites than its row lists is red until the count is
// lowered; and a row whose key has no site left is stale. Each message says how to
// shrink the row.
func (l *siteLedger) violations(t *testing.T, remedy string) []string {
	t.Helper()
	return l.violationsMode(t, remedy, allowlist.Updating())
}

// violationsMode fixes update mode for the temporary package-sharded fixture
// so its shrink-only assertions run even when the suite updates ledgers.
func (l *siteLedger) violationsMode(t *testing.T, remedy string, update bool) []string {
	t.Helper()
	measured := map[string]int{}
	for k, w := range l.sites {
		measured[k] = len(w)
	}
	var out []string
	res := allowlist.CheckPackagesCountedMode(t, l.allow, measured, update)
	for _, k := range res.Unlisted {
		for _, w := range l.sites[k] {
			out = append(out, fmt.Sprintf("%s: %s (no row in %s): %s", w, k, l.path, remedy))
		}
	}
	for _, c := range res.Over {
		out = append(out, fmt.Sprintf("%s lists %s at %d sites, but %d are there now (%s); a new site is not covered by a row that was written for fewer: %s", l.path, c.Key, c.Listed, c.Measured, strings.Join(l.sites[c.Key], "; "), remedy))
	}
	for _, c := range res.Lowered {
		out = append(out, fmt.Sprintf("%s lists %s at %d sites, but only %d are there now; lower the row's count to %d (the list only shrinks; NOVA_CI_UPDATE=1 lowers it)", l.path, c.Key, c.Listed, c.Measured, c.Measured))
	}
	for _, row := range res.Stale {
		out = append(out, fmt.Sprintf("%s lists %s, but no site of that key is there any more; delete the stale row (the list only shrinks; NOVA_CI_UPDATE=1 drops it)", l.path, row.Key))
	}
	sort.Strings(out)
	return out
}

// TestDiscardedRuleReadsTheThreeShapes proves the rule over source: each shape
// is read, a reasoned site is not, and the look-alikes (a builtin discarded, an
// error read before the return, a non-error set to nil) are not.
func TestDiscardedRuleReadsTheThreeShapes(t *testing.T) {
	t.Parallel()
	src := `package p

func f() (int, error) { return 0, nil }

func g() {
	_ = f2()
	_, _ = f()
	var err error
	_ = err
	// ignored: a best-effort cleanup
	_ = f2()
	_ = f2() // ignored: the close after a read that already succeeded
	_ = len("x")
	if err != nil {
		return
	}
	if err != nil {
		println(err.Error())
		return
	}
	err = nil
	var p *int
	p = nil
	_ = p
}

func f2() error { return nil }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(src, "\n")
	got := map[string]int{}
	for _, s := range discardedSites(fset, f) {
		if reasonedAt(lines, s.line) {
			continue
		}
		got[s.shape]++
	}
	want := map[string]int{shapeBlank: 3, shapeBareRet: 1, shapeErrNilled: 1}
	for shape, n := range want {
		if got[shape] != n {
			t.Errorf("%s: %d unreasoned sites, want %d (got %v)", shape, got[shape], n, got)
		}
	}
}

// scriptHideAllowlistPath is the shrink-only ledger of the script lines that
// throw a failure's words or exit away: one `file:shape` per row, a reason after
// it. fleet/ is being reworked, so its rows wait for that; a script row leaves
// when each of its lines carries `# ignored: <reason>` or is fixed.
const scriptHideAllowlistPath = "testdata/scripthide"

// scriptHideDirs are where the fleet plays, the scripts and the bench tools
// live; scriptHideExts the files there that run.
var (
	scriptHideDirs = []string{"fleet", "scripts", "infra", "tools"}
	scriptHideExts = []string{".sh", ".yml", ".yaml", ".j2", ".ps1"}
)

// scriptHideShapes are the shell and ansible spellings of a thrown-away failure:
// an exit forced to success, an error stream sent nowhere, a task that cannot
// fail. `command -v x >/dev/null 2>&1` asks a question whose answer is the exit,
// and is not one.
var scriptHideShapes = []struct {
	name string
	re   *regexp.Regexp
}{
	{"or-true", regexp.MustCompile(`\|\|\s*true\b`)},
	{"stderr-null", regexp.MustCompile(`2>\s*/dev/null|&>\s*/dev/null|>\s*/dev/null\s+2>&1|2>\$null`)},
	{"failed-when-false", regexp.MustCompile(`failed_when:\s*(false|no)\b`)},
	{"ignore-errors", regexp.MustCompile(`ignore_errors:\s*(true|yes)\b`)},
}

var commandProbeRe = regexp.MustCompile(`command -v \S+ >\s*/dev/null 2>&1`)

// TestNoScriptHidesAFailure is the discarded rule's half for the fleet plays and
// the scripts (the owner's rule, 2026-09-30: never fail silently, always leave
// a breadcrumb). Every line under fleet/, scripts/, infra/ and tools/ in a
// script or a play that forces an exit to success (`|| true`), sends an error
// stream nowhere (`2>/dev/null`, `>/dev/null 2>&1`), or makes a task unable to
// fail (`failed_when: false`, `ignore_errors: true`) carries `# ignored:
// <reason>` on the line or the line above, or its `file:shape` is in the ledger.
func TestNoScriptHidesAFailure(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	ledger := newSiteLedger(t, scriptHideAllowlistPath)
	for _, f := range tree.Files {
		if f.Go || !f.InAnyDir(scriptHideDirs...) || f.HasDirNamed("testdata") || strings.HasSuffix(f.Rel, "_test.sh") || !hasAnySuffix(f.Rel, scriptHideExts) {
			continue
		}
		raw, err := os.ReadFile(f.Path)
		if err != nil {
			t.Fatalf("%s: %v", f.Rel, err)
		}
		lines := strings.Split(string(raw), "\n")
		for i, line := range lines {
			for _, shape := range scriptHideLine(line) {
				if scriptReasonedAt(lines, i) {
					continue
				}
				ledger.add(f.Rel+":"+shape, fmt.Sprintf("%s:%d %q", f.Rel, i+1, strings.TrimSpace(line)))
			}
		}
	}
	for _, v := range ledger.violations(t, "the line throws a failure away; let it show, or say why it is safe with `# ignored: <reason>` on this line or the one above (a row's count only falls)") {
		t.Error(v)
	}
}

// scriptHideLine returns the shapes one line holds.
func scriptHideLine(line string) []string {
	probe := commandProbeRe.ReplaceAllString(line, "")
	var out []string
	for _, s := range scriptHideShapes {
		if s.re.MatchString(probe) {
			out = append(out, s.name)
		}
	}
	return out
}

// scriptReasonedAt reports `# ignored: <reason>` on line i (0-based) or above it.
func scriptReasonedAt(lines []string, i int) bool {
	for _, j := range []int{i, i - 1} {
		if j < 0 {
			continue
		}
		if _, after, ok := strings.Cut(lines[j], "# ignored:"); ok && strings.TrimSpace(after) != "" {
			return true
		}
	}
	return false
}

func hasAnySuffix(s string, suffixes []string) bool {
	for _, x := range suffixes {
		if strings.HasSuffix(s, x) {
			return true
		}
	}
	return false
}

// TestScriptHideRuleReadsTheShapes proves the line reader: each shape is read,
// a command -v probe is not, and a reasoned line is not.
func TestScriptHideRuleReadsTheShapes(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]int{
		`kill "$pid" 2>/dev/null || true`:          2,
		`if command -v go >/dev/null 2>&1; then`:   0,
		`  failed_when: false`:                     1,
		`  ignore_errors: yes`:                     1,
		`nova-secrets check >/dev/null 2>&1`:       1,
		`echo "no failure here" # || not a shape"`: 0,
	} {
		if got := len(scriptHideLine(line)); got != want {
			t.Errorf("scriptHideLine(%q) found %d shapes, want %d", line, got, want)
		}
	}
	if !scriptReasonedAt([]string{"# ignored: a probe whose answer is the exit", "kill -0 1 2>/dev/null"}, 1) {
		t.Error("a reason on the line above was not read")
	}
}

// TestSiteLedgerShrinksBySiteNotByRow proves the four never-silent ledgers are
// shrink-only by site: a site under a key a row already lists is red (the row's
// count is what the ledger allows), a fixed site asks for a lower count, a key with
// no row is red at each site, and a count that matches is quiet.
func TestSiteLedgerShrinksBySiteNotByRow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, contents := range map[string]string{
		"a.txt": "# ceiling: 1\na/a.go:f:blank 2 two sites\n",
		"b.txt": "# ceiling: 1\nb/b.sh:or-true 1 one site\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	allow, err := allowlist.LoadPackages(dir, countedLedger)
	if err != nil {
		t.Fatal(err)
	}
	run := func(sites map[string]int) []string {
		l := &siteLedger{path: dir, allow: allow, sites: map[string][]string{}}
		for k, n := range sites {
			for i := 0; i < n; i++ {
				l.add(k, fmt.Sprintf("%s#%d", k, i+1))
			}
		}
		return l.violationsMode(t, "REMEDY", false)
	}
	if got := run(map[string]int{"a/a.go:f:blank": 2, "b/b.sh:or-true": 1}); len(got) != 0 {
		t.Errorf("a ledger that matches is quiet, got %q", got)
	}
	if got := run(map[string]int{"a/a.go:f:blank": 3, "b/b.sh:or-true": 1}); len(got) != 1 || !strings.Contains(got[0], "lists a/a.go:f:blank at 2 sites, but 3 are there now") || !strings.Contains(got[0], "REMEDY") {
		t.Errorf("a new site under a listed key must be red with the remedy, got %q", got)
	}
	if got := run(map[string]int{"a/a.go:f:blank": 2, "b/b.sh:or-true": 2}); len(got) != 1 || !strings.Contains(got[0], "b/b.sh:or-true at 1 sites, but 2") {
		t.Errorf("a second site under a one-site row must be red, got %q", got)
	}
	if got := run(map[string]int{"a/a.go:f:blank": 1, "b/b.sh:or-true": 1}); len(got) != 1 || !strings.Contains(got[0], "lower the row's count to 1") {
		t.Errorf("a fixed site must ask for a lower count, got %q", got)
	}
	if got := run(map[string]int{"a/a.go:f:blank": 2, "b/b.sh:or-true": 1, "c/c.go:g:blank": 2}); len(got) != 2 || !strings.Contains(got[0], "c/c.go:g:blank#") {
		t.Errorf("a key with no row must be red at each of its sites, got %q", got)
	}
	if got := run(map[string]int{"a/a.go:f:blank": 2}); len(got) != 1 || !strings.Contains(got[0], "b/b.sh:or-true, but no site") {
		t.Errorf("a row with no site left is stale, got %q", got)
	}
}

// TestSiteLedgerNamesReasonlessShard proves a missing reason identifies its
// shard and line.
func TestSiteLedgerNamesReasonlessShard(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "cmd", "nova-test.txt")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("# ceiling: 1\ncmd/nova-test/main.go:f:blank 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	allow, err := allowlist.LoadPackages(dir, countedLedger)
	if err != nil {
		t.Fatal(err)
	}
	got := missingReasons(allow)
	if len(got) != 1 || !strings.Contains(got[0], file+":2:") || !strings.Contains(got[0], "carries no reason") {
		t.Errorf("reasonless row must name its shard and line, got %q", got)
	}
}
