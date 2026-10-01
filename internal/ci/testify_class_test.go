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
// every Go test uses github.com/stretchr/testify. That every test runs in parallel is held
// by TestEveryTestOpensWithTParallel and its serial-tests_allowlist.txt, not counted here.
// require carries setup and preconditions; assert carries the checks inside a table
// row, so one bad row does not hide the rest; a shared rig is a testify suite or a
// helper struct in the package's testkit; ErrorIs, ErrorAs, ErrorContains, Eventually,
// JSONEq, ElementsMatch, FileExists and Panics replace their hand-written
// equivalents; named cases run under t.Run; the environment and the working
// directory are injected through the code's config, never mutated with t.Setenv or a
// Chdir.
//
// The tree is measured per package, in two kinds of site:
//
//	assert    an `if` whose body calls t.Fatal, t.Fatalf, t.Error, t.Errorf, t.Fail or
//	          t.FailNow: `if err != nil { t.Fatal }`, `if got != want { t.Errorf }`,
//	          `if !strings.Contains(...) { t.Errorf }`, a reflect.DeepEqual guard
//	env       a call of t.Setenv, t.Chdir or os.Chdir
//
// testdata/testify/ holds one shard per package: each row is
// `<package>:<kind> <sites> <reason>`, and the count only falls. A package that
// measures more sites than its row is red, a package with no row and a site is red,
// and a row above what the package measures is red, so the change that converts a
// test lowers its row in the same commit. NOVA_CI_UPDATE=1 lowers the counts and
// drops the rows at zero, and never raises one or adds one.
const testifyLedgerPath = "testdata/testify"

// testifyKinds are the two kinds of site, in the order the ledger lists them.
var testifyKinds = []string{"assert", "env"}

// testifyRemedy is the one thing to do for each kind.
var testifyRemedy = map[string]string{
	"assert": "replace each site with its testify call: `if err != nil { t.Fatal }` -> require.NoError(t, err); `if err == nil` -> require.Error; `if got != want` -> assert.Equal (assert.NotEqual for ==); a nil guard -> assert.Nil or assert.NotNil; `!strings.Contains` -> assert.Contains; a reflect.DeepEqual guard -> assert.Equal, assert.ElementsMatch or assert.JSONEq; a length guard -> assert.Len or assert.Empty; any other bool -> assert.True or assert.False; require for setup and preconditions, assert inside table rows",
	"env":    "inject the value through the code's config or the child's own environment (cmd.Env) and open the test with t.Parallel(); t.Setenv and Chdir forbid it",
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

	l, err := allowlist.LoadPackages(testifyLedgerPath, allowlist.Options{Ceiling: true, Counted: true, PackageKeys: true})
	require.NoError(t, err)
	rows, err := testifyLedgerRows(l)
	require.NoError(t, err)

	update := allowlist.Updating()
	problems, kindsSeen, _ := testifyJudge(counts, byKey, rows, update)
	if len(problems) > 0 {
		var remedies []string
		for _, kind := range testifyKinds {
			if kindsSeen[kind] {
				remedies = append(remedies, kind+": "+testifyRemedy[kind])
			}
		}
		assert.Failf(t, "the testing rule", "%s\n%s\n(docs/STANDARD.md section 8; the ledger is %s)", strings.Join(problems, "\n"), strings.Join(remedies, "\n"), testifyLedgerPath)
		return
	}
	allowlist.CheckPackagesCountedMode(t, l, counts, update)
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
func testifyLedgerRows(l *allowlist.Packages) (map[string]int, error) {
	rows := map[string]int{}
	for _, shard := range l.Lists() {
		for _, r := range shard.Rows() {
			f := strings.Fields(r.Text)
			if len(f) < 3 {
				return nil, fmt.Errorf("%s:%d: %q is not `<package>:<kind> <sites> <reason>`", shard.Path, r.Line, r.Text)
			}
			n, err := strconv.Atoi(f[1])
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("%s:%d: %q: the sites are a positive number", shard.Path, r.Line, r.Text)
			}
			kind := f[0][strings.LastIndex(f[0], ":")+1:]
			if _, ok := testifyRemedy[kind]; !ok || !strings.Contains(f[0], ":") {
				return nil, fmt.Errorf("%s:%d: %q: the kind is one of %v", shard.Path, r.Line, f[0], testifyKinds)
			}
			if _, dup := rows[f[0]]; dup {
				return nil, fmt.Errorf("%s:%d: %s is listed twice", shard.Path, r.Line, f[0])
			}
			rows[f[0]] = n
		}
	}
	return rows, nil
}

// TestTestifyLedgerReadsPackageShards keeps the full package-kind key and
// requires a reason in the originating shard before the updater can run.
func TestTestifyLedgerReadsPackageShards(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cmdShard := filepath.Join(dir, "cmd", "nova-ci.txt")
	internalShard := filepath.Join(dir, "internal", "foo.txt")
	for _, file := range []string{cmdShard, internalShard} {
		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0700))
	}
	require.NoError(t, os.WriteFile(cmdShard, []byte("# ceiling: 1\ncmd/nova-ci:assert 2 bare assertions to convert\n"), 0600))
	require.NoError(t, os.WriteFile(internalShard, []byte("# ceiling: 1\ninternal/foo:env 1\n"), 0600))
	ledger, err := allowlist.LoadPackages(dir, allowlist.Options{Ceiling: true, Counted: true, PackageKeys: true})
	require.NoError(t, err)
	_, err = testifyLedgerRows(ledger)
	require.ErrorContains(t, err, internalShard+":2:")

	require.NoError(t, os.WriteFile(internalShard, []byte("# ceiling: 1\ninternal/foo:env 1 inject config\n"), 0600))
	ledger, err = allowlist.LoadPackages(dir, allowlist.Options{Ceiling: true, Counted: true, PackageKeys: true})
	require.NoError(t, err)
	rows, err := testifyLedgerRows(ledger)
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"cmd/nova-ci:assert": 2, "internal/foo:env": 1}, rows)
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
// environment and working-directory sites.
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
// testing.TB (a function's parameter), plus the conventional t and tb.
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
