package ci

import (
	"fmt"
	"go/ast"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// parallel_class_test.go is Glenn's rule of 2026-09-25: Go tests always run in
// parallel. Every top-level `func TestX(t *testing.T)` under cmd/ and internal/
// opens with `t.Parallel()` as its FIRST statement, so a package's tests share
// the machine instead of queueing behind one another; the per-commit legs
// (go-test-cmd, go-test-internal) are held to one minute, and a serial test is
// wall time every commit pays.
//
// A test that cannot run in parallel sits on the serial allowlist with its
// reason: `t.Setenv` and `t.Chdir` panic under t.Parallel, `os.Setenv` and
// `os.Chdir` change the whole process, and a test that swaps a package-level
// seam (`now = fake`) races every other test reading it. The list only
// shrinks: an entry whose test now opens with t.Parallel(), or whose test is
// gone, is a stale entry and a red run, and the list may never grow past the
// `# ceiling: N` line it carries. The way off the list is a per-test seam: cmd.Env for a
// child process, a field on the struct under test for a clock or a dialer, a
// t.TempDir for a path.
//
// The walk is syntactic and reads every _test.go regardless of build tags, so
// a darwin-only or slow-tagged test is held to the same rule.
const serialTestsAllowlistPath = "testdata/serial-tests_allowlist.txt"

func TestEveryTestOpensWithTParallel(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	allow := readSerialTestsAllowlist(t)
	// serial is the measured set: every test that does not open with
	// t.Parallel(). nowParallel names the listed ones that do, for the remedy.
	serial, nowParallel := map[string]bool{}, map[string]bool{}
	var violations []string
	total, parallel := 0, 0

	for _, f := range tree.GoFilesUnder(true, "cmd", "internal") {
		if f.HasDirNamed("testdata") {
			continue
		}
		if f.ParseErr != nil {
			t.Fatal(f.ParseErr)
		}
		for _, decl := range f.AST.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil || !isGoTestName(fn.Name.Name) {
				continue
			}
			tname, ok := testingTParam(fn)
			if !ok {
				continue
			}
			total++
			key := f.Rel + ":" + fn.Name.Name
			opens := len(fn.Body.List) > 0 && isParallelStmt(fn.Body.List[0], tname)
			if opens {
				parallel++
				nowParallel[key] = true
				continue
			}
			serial[key] = true
			if allow.Has(key) {
				continue
			}
			violations = append(violations, fmt.Sprintf(
				"%s:%d: %s does not open with %s.Parallel(); make it the first statement, or give the test a per-test seam (cmd.Env, an injected clock, t.TempDir) instead of t.Setenv, os.Chdir or a package-level swap",
				f.Rel, tree.FSet.Position(fn.Pos()).Line, fn.Name.Name, tname))
		}
	}
	if _, ok := allow.Ceiling(); !ok {
		violations = append(violations, fmt.Sprintf(
			"%s carries no `# ceiling: N` line; the serial list's count only falls, and the ceiling is what holds it there",
			serialTestsAllowlistPath))
	}
	for _, row := range allowlist.Check(t, allow, serial).Stale {
		gone := "no such test exists any more"
		if nowParallel[row.Key] {
			gone = "it opens with t.Parallel() now"
		}
		violations = append(violations, fmt.Sprintf(
			"%s lists %s, but %s; delete the stale entry and lower its ceiling line (the list only shrinks; NOVA_CI_UPDATE=1 does both)",
			serialTestsAllowlistPath, row.Key, gone))
	}
	if total == 0 {
		t.Fatal("no test function found under cmd/ or internal/; the walk is broken, not the tree")
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
	t.Logf("%d of %d test functions open with t.Parallel(); %d on the serial allowlist", parallel, total, allow.Len())
}

// isGoTestName is go test's own rule: Test, then nothing or a character that
// is not a lower-case letter. TestMain is the package's harness, not a test.
func isGoTestName(name string) bool {
	if !strings.HasPrefix(name, "Test") || name == "TestMain" {
		return false
	}
	if len(name) == len("Test") {
		return true
	}
	c := name[len("Test")]
	return c < 'a' || c > 'z'
}

// testingTParam reports the name of the one *testing.T parameter, or false
// when the function is not a test's signature.
func testingTParam(fn *ast.FuncDecl) (string, bool) {
	params := fn.Type.Params.List
	if len(params) != 1 || fn.Type.Results != nil {
		return "", false
	}
	star, ok := params[0].Type.(*ast.StarExpr)
	if !ok {
		return "", false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "T" {
		return "", false
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "testing" {
		return "", false
	}
	if len(params[0].Names) == 0 {
		return "_", true
	}
	return params[0].Names[0].Name, true
}

// isParallelStmt reports whether s is the statement `<tname>.Parallel()`.
func isParallelStmt(s ast.Stmt, tname string) bool {
	es, ok := s.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Parallel" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == tname
}

// readSerialTestsAllowlist reads `path:TestName  serial: <reason>` lines; a
// line without a reason is refused, so every serial test says why.
func readSerialTestsAllowlist(t *testing.T) *allowlist.List {
	t.Helper()
	allow := loadAllowlist(t, serialTestsAllowlistPath, shrinkOnly)
	first := map[string]int{}
	for _, row := range allow.Rows() {
		_, reason, _ := strings.Cut(row.Text, " ")
		reason = strings.TrimSpace(reason)
		if !strings.HasPrefix(reason, "serial: ") || len(reason) == len("serial: ") {
			t.Errorf("%s:%d: %q carries no `serial: <reason>`", serialTestsAllowlistPath, row.Line, row.Text)
		}
		if at, dup := first[row.Key]; dup {
			t.Errorf("%s:%d: %s is listed twice (first at line %d)", serialTestsAllowlistPath, row.Line, row.Key, at)
			continue
		}
		first[row.Key] = row.Line
	}
	return allow
}
