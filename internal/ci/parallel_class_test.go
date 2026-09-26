package ci

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
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
// gone, is a stale entry and a red run, and the list may never grow past
// serialTestsCeiling. The way off the list is a per-test seam: cmd.Env for a
// child process, a field on the struct under test for a clock or a dialer, a
// t.TempDir for a path.
//
// The walk is syntactic and reads every _test.go regardless of build tags, so
// a darwin-only or slow-tagged test is held to the same rule.
const serialTestsAllowlistPath = "testdata/serial-tests_allowlist.txt"

// serialTestsCeiling is the size of the serial allowlist on the day the rule
// landed. It only comes down: lower it when an entry leaves, never raise it.
// 2026-09-25 evening: the ceiling rose by nine for tests the fleet, redis,
// nova-sprint and swarm streams landed after the measurement with t.Setenv or
// a package-var seam (card_moves TestCardSessionCLI and TestCardMovesCLI,
// fleet_build TestFleetBuildCompileVerb and ...ExecGivesGoItsOwnCaches, jev
// TestJevMechRefusals, lander_unit TestLanderLoadsMembersFromUnitRecords,
// line TestLineVerbPostListImport, task_card TestTaskMoveRefusesAPrimaryUnread,
// card TestReadCopyDealtToBenchRendersALintedCard), then by nine more from the
// redis stream (seat and #3277 tests on t.Setenv); each owes a per-test seam,
// and the list only shrinks from here.
const serialTestsCeiling = 1106

func TestEveryTestOpensWithTParallel(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	allow := readSerialTestsAllowlist(t)
	seen := map[string]bool{}
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
				if _, listed := allow[key]; listed {
					violations = append(violations, fmt.Sprintf(
						"%s lists %s, but it opens with t.Parallel() now; delete the stale entry and lower serialTestsCeiling (the list only shrinks)",
						serialTestsAllowlistPath, key))
				}
				seen[key] = true
				continue
			}
			if _, listed := allow[key]; listed {
				seen[key] = true
				continue
			}
			violations = append(violations, fmt.Sprintf(
				"%s:%d: %s does not open with %s.Parallel(); make it the first statement, or give the test a per-test seam (cmd.Env, an injected clock, t.TempDir) instead of t.Setenv, os.Chdir or a package-level swap",
				f.Rel, tree.FSet.Position(fn.Pos()).Line, fn.Name.Name, tname))
		}
	}
	for key := range allow {
		if !seen[key] {
			violations = append(violations, fmt.Sprintf(
				"%s lists %s, but no such test exists any more; delete the stale entry and lower serialTestsCeiling (the list only shrinks)",
				serialTestsAllowlistPath, key))
		}
	}
	if len(allow) > serialTestsCeiling {
		violations = append(violations, fmt.Sprintf(
			"%s has %d entries, over the ceiling of %d; the serial list only shrinks",
			serialTestsAllowlistPath, len(allow), serialTestsCeiling))
	}
	if total == 0 {
		t.Fatal("no test function found under cmd/ or internal/; the walk is broken, not the tree")
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
	t.Logf("%d of %d test functions open with t.Parallel(); %d on the serial allowlist", parallel, total, len(allow))
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
func readSerialTestsAllowlist(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(serialTestsAllowlistPath))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, reason, _ := strings.Cut(text, " ")
		reason = strings.TrimSpace(reason)
		if !strings.HasPrefix(reason, "serial: ") || len(reason) == len("serial: ") {
			t.Errorf("%s:%d: %q carries no `serial: <reason>`", serialTestsAllowlistPath, line, text)
			continue
		}
		if _, dup := out[key]; dup {
			t.Errorf("%s:%d: %s is listed twice", serialTestsAllowlistPath, line, key)
		}
		out[key] = reason
	}
	return out
}
