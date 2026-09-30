package ci

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// testify_class_test.go holds the standard's testing rule (docs/STANDARD.md, section 8):
// every Go test uses github.com/stretchr/testify, and every test runs in parallel.
// require carries setup and preconditions; assert carries the checks inside a table
// row, so one bad row does not hide the rest; a shared rig is a testify suite or a
// helper struct in the package's testkit; ErrorIs, ErrorAs, ErrorContains, Eventually,
// JSONEq, ElementsMatch, FileExists and Panics replace their hand-written
// equivalents; named cases run under t.Run; the environment and the working
// directory are injected through the code's config, never mutated with t.Setenv or a
// Chdir.
//
// The tree is measured per package, in three kinds of site:
//
//	assert    an `if` whose body calls t.Fatal, t.Fatalf, t.Error, t.Errorf, t.Fail or
//	          t.FailNow: `if err != nil { t.Fatal }`, `if got != want { t.Errorf }`,
//	          `if !strings.Contains(...) { t.Errorf }`, a reflect.DeepEqual guard
//	parallel  a file with a top-level test function that does not open with t.Parallel()
//	env       a call of t.Setenv, t.Chdir or os.Chdir
//
// testdata/testify_allowlist.txt is the ledger: one `<package>:<kind> <sites> <reason>`
// row per package and kind still short, and the count only falls. A package that
// measures more sites than its row is red, a package with no row and a site is red,
// and a row above what the package measures is red, so the change that converts a
// test lowers its row in the same commit. NOVA_CI_UPDATE=1 lowers the counts and
// drops the rows at zero, and never raises one or adds one.
const testifyLedgerPath = "testdata/testify_allowlist.txt"

// testifyKinds are the three kinds of site, in the order the ledger lists them.
var testifyKinds = []string{"assert", "parallel", "env"}

// testifyRemedy is the one thing to do for each kind.
var testifyRemedy = map[string]string{
	"assert":   "replace each site with its testify call: `if err != nil { t.Fatal }` -> require.NoError(t, err); `if err == nil` -> require.Error; `if got != want` -> assert.Equal (assert.NotEqual for ==); a nil guard -> assert.Nil or assert.NotNil; `!strings.Contains` -> assert.Contains; a reflect.DeepEqual guard -> assert.Equal, assert.ElementsMatch or assert.JSONEq; a length guard -> assert.Len or assert.Empty; any other bool -> assert.True or assert.False; require for setup and preconditions, assert inside table rows",
	"parallel": "open every test with t.Parallel() and inject the environment and the working directory through the code's config",
	"env":      "inject the value through the code's config or the child's own environment (cmd.Env) and open the test with t.Parallel(); t.Setenv and Chdir forbid it",
}

// testifyShapeRemedy is the testify call for each shape of assert site.
var testifyShapeRemedy = map[string]string{
	"err":       "require.NoError / require.Error / assert.ErrorIs",
	"nil":       "assert.Nil / assert.NotNil",
	"compare":   "assert.Equal / assert.NotEqual / assert.Greater",
	"len":       "assert.Len / assert.Empty",
	"contains":  "assert.Contains / assert.NotContains",
	"deepequal": "assert.Equal / assert.ElementsMatch / assert.JSONEq",
	"bool":      "assert.True / assert.False",
	"compound":  "one assert per condition",
	"other":     "the matching assert",
}

// testifySite is one measured site.
type testifySite struct {
	Pkg, Kind, Where, Shape string
}

func (s testifySite) key() string { return s.Pkg + ":" + s.Kind }

func TestTestsUseTestify(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	var sites []testifySite
	for _, f := range tree.GoFilesUnder(true, "cmd", "internal") {
		if f.HasDirNamed("testdata") {
			continue
		}
		require.NoError(t, f.ParseErr, f.Rel)
		sites = append(sites, testifySitesInFile(tree.FSet, f.AST, f.Rel)...)
	}
	counts, byKey := map[string]int{}, map[string][]testifySite{}
	for _, s := range sites {
		counts[s.key()]++
		byKey[s.key()] = append(byKey[s.key()], s)
	}

	l := loadAllowlist(t, testifyLedgerPath, shrinkOnly)
	rows, err := testifyLedgerRows(l)
	require.NoError(t, err)

	update := allowlist.Updating()
	problems, kindsSeen, lowered := testifyJudge(counts, byKey, rows, update)
	if len(problems) > 0 {
		var remedies []string
		for _, kind := range testifyKinds {
			if kindsSeen[kind] {
				remedies = append(remedies, kind+": "+testifyRemedy[kind])
			}
		}
		assert.Failf(t, "the testing rule", "%s\n%s\n(docs/STANDARD.md section 8; the ledger is %s)", strings.Join(problems, "\n"), strings.Join(remedies, "\n"), testifyLedgerPath)
	}
	if !update || len(lowered) == 0 {
		return
	}
	out, changed := testifyRewrite(l.Text(), lowered)
	if !changed {
		return
	}
	require.NoError(t, testifyWriteLedger(l.Path, out))
	assert.Fail(t, allowlist.UpdatedRerun, fmt.Sprintf("%s: %d rows lowered or dropped", l.Path, len(lowered)))
}

// testifyJudge compares the measured counts with the ledger: the problems to print,
// the kinds they concern, and the rows an update lowers (a new count of 0 drops the row).
func testifyJudge(counts map[string]int, byKey map[string][]testifySite, rows map[string]int, update bool) (problems []string, kindsSeen map[string]bool, lowered map[string]int) {
	kindsSeen, lowered = map[string]bool{}, map[string]int{}
	for _, key := range sortedKeys(counts) {
		n := counts[key]
		row, listed := rows[key]
		kind := key[strings.LastIndex(key, ":")+1:]
		switch {
		case !listed:
			kindsSeen[kind] = true
			problems = append(problems, fmt.Sprintf("%s: %d sites and no row; the ledger gains no row\n%s", key, n, testifyExamples(byKey[key], 5)))
		case n > row:
			kindsSeen[kind] = true
			problems = append(problems, fmt.Sprintf("%s: %d sites, over its ledger of %d; the count only falls; the new sites are in the files your change touches, the package's first five are:\n%s", key, n, row, testifyExamples(byKey[key], 5)))
		case n < row:
			lowered[key] = n
			if !update {
				problems = append(problems, fmt.Sprintf("%s: the tree has %d sites, the ledger %d; lower the row (NOVA_CI_UPDATE=1 does it)", key, n, row))
			}
		}
	}
	for _, key := range sortedKeys(rows) {
		if counts[key] == 0 {
			lowered[key] = 0
			if !update {
				problems = append(problems, fmt.Sprintf("%s: %d in the ledger and none in the tree; delete the row (NOVA_CI_UPDATE=1 does it)", key, rows[key]))
			}
		}
	}
	return problems, kindsSeen, lowered
}

// testifyLedgerRows reads the `key sites reason` rows: the key is the first field,
// the count the second, and a row with no reason is refused so every package says why.
func testifyLedgerRows(l *allowlist.List) (map[string]int, error) {
	rows := map[string]int{}
	for _, r := range l.Rows() {
		f := strings.Fields(r.Text)
		if len(f) < 3 {
			return nil, fmt.Errorf("%s:%d: %q is not `<package>:<kind> <sites> <reason>`", l.Path, r.Line, r.Text)
		}
		n, err := strconv.Atoi(f[1])
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("%s:%d: %q: the sites are a positive number", l.Path, r.Line, r.Text)
		}
		kind := f[0][strings.LastIndex(f[0], ":")+1:]
		if _, ok := testifyRemedy[kind]; !ok || !strings.Contains(f[0], ":") {
			return nil, fmt.Errorf("%s:%d: %q: the kind is one of %v", l.Path, r.Line, f[0], testifyKinds)
		}
		if _, dup := rows[f[0]]; dup {
			return nil, fmt.Errorf("%s:%d: %s is listed twice", l.Path, r.Line, f[0])
		}
		rows[f[0]] = n
	}
	return rows, nil
}

// testifyRewrite lowers the counts and drops the rows the map names (0 drops), every
// other line byte for byte; it never raises a count.
func testifyRewrite(text string, lowered map[string]int) (string, bool) {
	lines := strings.Split(text, "\n")
	var out []string
	changed := false
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) < 3 || strings.HasPrefix(f[0], "#") {
			out = append(out, line)
			continue
		}
		n, ok := lowered[f[0]]
		old, err := strconv.Atoi(f[1])
		if !ok || err != nil || n >= old {
			out = append(out, line)
			continue
		}
		changed = true
		if n == 0 {
			continue
		}
		out = append(out, strings.Replace(line, " "+f[1]+" ", " "+strconv.Itoa(n)+" ", 1))
	}
	return strings.Join(out, "\n"), changed
}

// testifyWriteLedger replaces the ledger through a temp file in its own directory.
func testifyWriteLedger(p, text string) error {
	f, err := os.CreateTemp(filepath.Dir(p), "."+filepath.Base(p)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.WriteString(text); err != nil {
		return fmt.Errorf("%w (close: %v, remove: %v)", err, f.Close(), os.Remove(tmp))
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("%w (remove: %v)", err, os.Remove(tmp))
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return fmt.Errorf("%w (remove: %v)", err, os.Remove(tmp))
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("%w (remove: %v)", err, os.Remove(tmp))
	}
	return nil
}

// testifyExamples lists up to max sites with the testify call each shape wants.
func testifyExamples(sites []testifySite, max int) string {
	var b strings.Builder
	for i, s := range sites {
		if i == max {
			fmt.Fprintf(&b, "  ... and %d more\n", len(sites)-max)
			break
		}
		if s.Shape == "" {
			fmt.Fprintf(&b, "  %s\n", s.Where)
			continue
		}
		fmt.Fprintf(&b, "  %s (%s): %s\n", s.Where, s.Shape, testifyShapeRemedy[s.Shape])
	}
	return strings.TrimRight(b.String(), "\n")
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// testifySitesInFile measures one parsed test file: the bare assertion sites, the
// environment and working-directory sites, and whether the file has a test that does
// not open with t.Parallel() (one site for the file).
func testifySitesInFile(fset *token.FileSet, file *ast.File, rel string) []testifySite {
	pkg := path.Dir(rel)
	names := testingNames(file)
	at := func(p token.Pos) string { return fmt.Sprintf("%s:%d", rel, fset.Position(p).Line) }
	var sites []testifySite

	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.IfStmt:
			if ifBodyFails(n.Body, names) {
				sites = append(sites, testifySite{Pkg: pkg, Kind: "assert", Where: at(n.Pos()), Shape: testifyShape(n.Cond)})
			}
		case *ast.CallExpr:
			if isEnvSite(n, names) {
				sites = append(sites, testifySite{Pkg: pkg, Kind: "env", Where: at(n.Pos())})
			}
		}
		return true
	})

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Body == nil || !isGoTestName(fn.Name.Name) {
			continue
		}
		tname, ok := testingTParam(fn)
		if !ok {
			continue
		}
		if len(fn.Body.List) == 0 || !isParallelStmt(fn.Body.List[0], tname) {
			sites = append(sites, testifySite{Pkg: pkg, Kind: "parallel", Where: at(fn.Pos())})
			break
		}
	}
	return sites
}

// isEnvSite reports a call of t.Setenv, t.Chdir or os.Chdir.
func isEnvSite(call *ast.CallExpr, names map[string]bool) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	recv, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	switch sel.Sel.Name {
	case "Setenv":
		return names[recv.Name]
	case "Chdir":
		return names[recv.Name] || recv.Name == "os"
	}
	return false
}

// testingNames are the identifiers the file binds to a *testing.T, *testing.B or
// testing.TB (a function's parameter), plus the conventional t, tb and b.
func testingNames(file *ast.File) map[string]bool {
	names := map[string]bool{"t": true, "tb": true}
	ast.Inspect(file, func(n ast.Node) bool {
		var ft *ast.FuncType
		switch n := n.(type) {
		case *ast.FuncDecl:
			ft = n.Type
		case *ast.FuncLit:
			ft = n.Type
		default:
			return true
		}
		for _, p := range ft.Params.List {
			typ := p.Type
			if star, ok := typ.(*ast.StarExpr); ok {
				typ = star.X
			}
			sel, ok := typ.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "testing" && (sel.Sel.Name == "T" || sel.Sel.Name == "B" || sel.Sel.Name == "TB") {
				for _, name := range p.Names {
					names[name.Name] = true
				}
			}
		}
		return true
	})
	return names
}

// ifBodyFails reports whether a statement of the block calls a failing method of a
// testing value.
func ifBodyFails(body *ast.BlockStmt, names map[string]bool) bool {
	for _, s := range body.List {
		es, ok := s.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := es.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok || !names[recv.Name] {
			continue
		}
		switch sel.Sel.Name {
		case "Fatal", "Fatalf", "Error", "Errorf", "Fail", "FailNow":
			return true
		}
	}
	return false
}

// testifyShape names the shape of an `if` condition, for the remedy.
func testifyShape(cond ast.Expr) string {
	switch c := cond.(type) {
	case *ast.ParenExpr:
		return testifyShape(c.X)
	case *ast.UnaryExpr:
		if c.Op == token.NOT {
			if call, ok := c.X.(*ast.CallExpr); ok {
				switch selectorCallName(call) {
				case "strings.Contains", "strings.HasPrefix", "strings.HasSuffix", "bytes.Contains", "slices.Contains":
					return "contains"
				case "reflect.DeepEqual", "slices.Equal", "maps.Equal", "bytes.Equal":
					return "deepequal"
				}
			}
			return "bool"
		}
	case *ast.BinaryExpr:
		switch c.Op {
		case token.LAND, token.LOR:
			return "compound"
		case token.EQL, token.NEQ, token.LSS, token.GTR, token.LEQ, token.GEQ:
			if isNilIdent(c.X) || isNilIdent(c.Y) {
				other := c.X
				if isNilIdent(c.X) {
					other = c.Y
				}
				if id, ok := other.(*ast.Ident); ok && strings.Contains(strings.ToLower(id.Name), "err") {
					return "err"
				}
				return "nil"
			}
			if isLenCall(c.X) || isLenCall(c.Y) {
				return "len"
			}
			return "compare"
		}
	case *ast.CallExpr:
		if selectorCallName(c) == "reflect.DeepEqual" {
			return "deepequal"
		}
		return "bool"
	case *ast.Ident:
		return "bool"
	}
	return "other"
}

func isNilIdent(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}

func isLenCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := call.Fun.(*ast.Ident)
	return ok && id.Name == "len"
}

// selectorCallName is `pkg.Func` for a selector call, and "" for anything else.
func selectorCallName(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if id, ok := sel.X.(*ast.Ident); ok {
		return id.Name + "." + sel.Sel.Name
	}
	return ""
}
