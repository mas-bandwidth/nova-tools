package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CLASS RULE: A TEST BINARY THAT RE-EXECS ITSELF HAS THE GUARD
// (docs/TESTS.md, tests-reexec-guard-everywhere.w1).
//
// A test that runs "this binary" (os.Executable, os.Args[0]) runs the TEST
// binary. Given CLI words it does not know as flags, a Go test binary runs the
// whole suite again, which reaches the same test, which runs the binary again:
// 289 processes deep on one machine, 2026-10-04. internal/testbin.Guard, called
// by a package-level `var _ = testbin.Guard(...)` in the package's reexec_test.go,
// refuses CLI words nothing answers and a chain past testbin.MaxDepth. The rule:
// every _test.go file under cmd/ that execs os.Executable() or os.Args[0] sits in
// a package that calls testbin.Guard. A red run names the file and line.

// reexecSites are the lines of one file that exec the test binary: a call of
// os.Executable and an index os.Args[0].
func reexecSites(fset *token.FileSet, f *ast.File) []token.Position {
	var out []token.Position
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if isSel(x.Fun, "os", "Executable") {
				out = append(out, fset.Position(x.Pos()))
			}
		case *ast.IndexExpr:
			if lit, ok := x.Index.(*ast.BasicLit); ok && lit.Value == "0" && isSel(x.X, "os", "Args") {
				out = append(out, fset.Position(x.Pos()))
			}
		}
		return true
	})
	return out
}

// callsGuard reports whether the file calls testbin.Guard.
func callsGuard(f *ast.File) bool {
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && isSel(c.Fun, "testbin", "Guard") {
			found = true
		}
		return !found
	})
	return found
}

func isSel(e ast.Expr, pkg, name string) bool {
	s, ok := e.(*ast.SelectorExpr)
	if !ok || s.Sel.Name != name {
		return false
	}
	id, ok := s.X.(*ast.Ident)
	return ok && id.Name == pkg
}

// unguardedReexecs takes test files by path (slash-separated) and their source,
// and returns one line per exec of the test binary in a directory where no file
// calls testbin.Guard, sorted: "<path>:<line>: ...". A file that does not parse
// is a line too: a rule that skips what it cannot read passes anything.
func unguardedReexecs(files map[string]string) []string {
	fset := token.NewFileSet()
	type found struct {
		path  string
		sites []token.Position
	}
	guarded := map[string]bool{}
	var all []found
	var out []string
	for path, src := range files {
		f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			out = append(out, fmt.Sprintf("%s:1: does not parse: %v", path, err))
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(path))
		if callsGuard(f) {
			guarded[dir] = true
		}
		if sites := reexecSites(fset, f); len(sites) > 0 {
			all = append(all, found{path, sites})
		}
	}
	for _, f := range all {
		if guarded[filepath.ToSlash(filepath.Dir(f.path))] {
			continue
		}
		for _, s := range f.sites {
			out = append(out, fmt.Sprintf("%s:%d: execs the test binary and %s has no testbin.Guard (add reexec_test.go with `var _ = testbin.Guard(\"<tool>\", nil)`; docs/TESTS.md, tests-reexec-guard-everywhere.w1)", f.path, s.Line, filepath.ToSlash(filepath.Dir(f.path))))
		}
	}
	sort.Strings(out)
	return out
}

func TestEveryReexecOfTheTestBinaryHasTheGuard(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	matches, err := filepath.Glob(filepath.Join(root, "cmd", "*", "*_test.go"))
	require.NoError(t, err)
	require.NotEmpty(t, matches, "no cmd test files found: an empty set is not a pass")
	files := map[string]string{}
	for _, m := range matches {
		rel, err := filepath.Rel(root, m)
		require.NoError(t, err)
		files[filepath.ToSlash(rel)] = readFile(t, m)
	}
	sites := 0
	for path, src := range files {
		fset := token.NewFileSet()
		if f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution); err == nil {
			sites += len(reexecSites(fset, f))
		}
	}
	require.NotZero(t, sites, "no re-exec of the test binary found under cmd: the scan matches nothing")
	assert.Empty(t, strings.Join(unguardedReexecs(files), "\n"), "every file that execs the test binary needs its package's testbin.Guard")
}

func TestTheReexecClassRefusesAnUnguardedFixtureAndNamesFileAndLine(t *testing.T) {
	t.Parallel()
	const reexec = "package main\n\nimport (\n\t\"os\"\n\t\"os/exec\"\n)\n\nfunc child() {\n\tself, _ := os.Executable()\n\texec.Command(self, \"init\").Run()\n}\n"
	const argv0 = "package main\n\nimport (\n\t\"os\"\n\t\"os/exec\"\n)\n\nfunc child() { exec.Command(os.Args[0], \"init\").Run() }\n"
	const guard = "package main\n\nimport \"github.com/mas-bandwidth/nova-tools/internal/testbin\"\n\nvar _ = testbin.Guard(\"x\", nil)\n"
	const noguard = "package main\n\nvar _ = Guard(\"x\", nil)\n\nfunc other() { _ = os.Args[1] }\n"

	got := unguardedReexecs(map[string]string{"cmd/x/a_test.go": reexec, "cmd/x/b_test.go": argv0})
	require.Len(t, got, 2)
	assert.Contains(t, got[0], "cmd/x/a_test.go:9:", "os.Executable, at its line")
	assert.Contains(t, got[1], "cmd/x/b_test.go:8:", "os.Args[0], at its line")
	assert.Contains(t, got[0], "no testbin.Guard")

	assert.Empty(t, unguardedReexecs(map[string]string{"cmd/x/a_test.go": reexec, "cmd/x/reexec_test.go": guard}), "the package's guard covers its files")
	assert.Len(t, unguardedReexecs(map[string]string{"cmd/x/a_test.go": reexec, "cmd/y/reexec_test.go": guard}), 1, "another package's guard covers nothing")
	assert.Len(t, unguardedReexecs(map[string]string{"cmd/x/a_test.go": reexec, "cmd/x/reexec_test.go": noguard}), 1, "a Guard that is not testbin's covers nothing")
	assert.Len(t, unguardedReexecs(map[string]string{"cmd/x/a_test.go": "package main\nfunc ("}), 1, "a file that does not parse is a line")
	assert.True(t, strings.HasPrefix(unguardedReexecs(map[string]string{"cmd/x/a_test.go": reexec})[0], "cmd/x/a_test.go:"))
}
