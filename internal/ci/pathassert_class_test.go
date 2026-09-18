package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// pathAssertAllowlistPath is the shrink-only list of the path-against-literal
// assertions this repository still permits. Every entry is checked in BOTH
// directions — an assertion not listed is a red run, and a listed assertion that
// has left is a stale entry and also a red run — so the list can only ever get
// shorter. It lives in testdata so a reader sees the whole exception set without
// reading the test.
const pathAssertAllowlistPath = "testdata/pathassert_allowlist.txt"

// TestNoTestComparesAPathAgainstASlashLiteral is the class rule behind the
// Windows leg this repository now runs on every pull request: a test that
// compares a filesystem path against a string literal with a "/" in it asserts
// the SEPARATOR, not the behaviour. It passes on Linux and macOS and fails on
// Windows, where the same path comes back with backslashes — one of the three
// shapes (with an execute-bit assertion and an unsuffixed fake .exe) that made
// up every Windows-only red the merge group has seen. The fix is always the
// same: compare filepath.ToSlash(got) against the slash literal, or build the
// want side with filepath.Join.
//
// THE HEURISTIC, stated plainly, because a class test with false positives is
// worse than none:
//
//	It flags a comparison — `==`, `!=`, or strings.Contains / HasPrefix /
//	HasSuffix / EqualFold — where ONE side is a string literal containing "/"
//	and the OTHER side is PATH-VALUED, inside an `if` whose body reports with
//	t.Errorf or t.Fatalf.
//
// Path-valued is deliberately narrow: the expression is a call to one of the
// path producers below (filepath.Join, filepath.Abs, filepath.Rel, filepath.Dir,
// filepath.Base, filepath.Clean, filepath.EvalSymlinks, os.Getwd, os.MkdirTemp,
// os.Executable, t.TempDir), or an identifier assigned from one of them earlier
// in the same function. So a test comparing an ordinary string — a log line, a
// URL, a package path, a JSON field — against a literal with a slash in it is
// never flagged, because nothing says that string is a path.
//
// It passes, and the assertion is portable, when the path side goes through
// filepath.ToSlash, or when the want side is built with filepath.Join instead of
// written as a literal (a literal inside a Join call is not a comparison
// operand, so it is not read at all). A URL literal ("https://…") is not a path
// and is skipped.
//
// What it deliberately does NOT see: a path held in a struct field or returned
// through a helper, a comparison written without an `if`, a table-driven case
// whose want column carries a slash. Those are real instances of the same class
// and this test is silent about them. That is the trade: few false positives at
// the cost of some false negatives, and the Windows leg on the PR is what
// catches the rest.
func TestNoTestComparesAPathAgainstASlashLiteral(t *testing.T) {
	root := repoRoot(t)
	allow := readPathAssertAllowlist(t)
	seen := map[string]bool{}
	var violations []string

	for _, dir := range []string{"cmd", "internal"} {
		base := filepath.Join(root, dir)
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, raw, 0)
			if err != nil {
				return err
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				key := rel + ":" + removeAllFuncName(fn)
				for _, f := range flaggedPathAssertions(fn) {
					seen[key] = true
					if allow[key] {
						continue
					}
					violations = append(violations, fmt.Sprintf(
						"%s:%d: a path is compared against the literal %q; on Windows that path comes back with backslashes. Compare filepath.ToSlash(got) against the literal, or build the want side with filepath.Join. (%s holds the exceptions, and only shrinks.)",
						rel, fset.Position(f.pos).Line, f.literal, pathAssertAllowlistPath))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// The list only shrinks: an entry whose assertion has left is a red run, so
	// nobody can quietly widen the exception set and leave it there.
	for key := range allow {
		if !seen[key] {
			violations = append(violations, fmt.Sprintf(
				"%s lists %s, but no path-against-literal comparison is there any more; delete the stale entry (the list only shrinks)",
				pathAssertAllowlistPath, key))
		}
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// pathAssertion is one flagged comparison: the slash literal a path was compared
// against, and where the comparison is.
type pathAssertion struct {
	literal string
	pos     token.Pos
}

// flaggedPathAssertions is the heuristic itself, in one place so the walk above
// and TestPathAssertHeuristicReadsWhatItClaims below read exactly the same rule.
func flaggedPathAssertions(fn *ast.FuncDecl) []pathAssertion {
	paths := pathValuedVars(fn)
	var out []pathAssertion
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		stmt, ok := n.(*ast.IfStmt)
		if !ok || !reportsWithErrorf(stmt.Body) {
			return true
		}
		for _, lit := range slashLiteralsComparedToAPath(stmt.Cond, paths) {
			out = append(out, pathAssertion{literal: lit, pos: stmt.Cond.Pos()})
		}
		return true
	})
	return out
}

// pathProducers are the calls whose result is a host filesystem path, written as
// `pkg.Func`. The list is short on purpose: every name here is one whose result
// carries the platform's separator.
var pathProducers = map[string]bool{
	"filepath.Join":         true,
	"filepath.Abs":          true,
	"filepath.Rel":          true,
	"filepath.Dir":          true,
	"filepath.Base":         true,
	"filepath.Clean":        true,
	"filepath.EvalSymlinks": true,
	"os.Getwd":              true,
	"os.MkdirTemp":          true,
	"os.Executable":         true,
}

// pathValuedVars is the set of identifiers in fn assigned from a path producer:
// `p := filepath.Join(...)`, `dir := t.TempDir()`, and the same through a
// multi-value assignment. An identifier assigned from filepath.ToSlash is NOT in
// the set — that is the portable form this rule asks for, and a comparison
// against it is exactly right.
func pathValuedVars(fn *ast.FuncDecl) map[string]bool {
	vars := map[string]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok || !isPathProducer(call) {
			return true
		}
		for _, lhs := range assign.Lhs {
			if id, ok := lhs.(*ast.Ident); ok && id.Name != "_" {
				vars[id.Name] = true
			}
		}
		return true
	})
	return vars
}

// isPathProducer reports whether call is one of the path producers above, or the
// testing helper t.TempDir(), whose result is a host path like any other.
func isPathProducer(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	if sel.Sel.Name == "TempDir" {
		return true
	}
	return pathProducers[id.Name+"."+sel.Sel.Name]
}

// isPathValued reports whether expr is a path producer's call or an identifier
// this function assigned from one. filepath.ToSlash(x) is deliberately not
// path-valued: it is the portable form.
func isPathValued(expr ast.Expr, paths map[string]bool) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return paths[e.Name]
	case *ast.CallExpr:
		return isPathProducer(e)
	case *ast.ParenExpr:
		return isPathValued(e.X, paths)
	}
	return false
}

// slashLiteral returns the value of expr when it is a string literal containing
// a "/" that is not part of a URL scheme, and "" otherwise. A literal that is
// only a separator ("/") says nothing about a layout and is skipped.
func slashLiteral(expr ast.Expr) string {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil || !strings.Contains(v, "/") || strings.Contains(v, "://") {
		return ""
	}
	if strings.Trim(v, "/") == "" {
		return ""
	}
	return v
}

// stringFuncs are the string tests this rule reads beside == and !=: each takes
// the value first and the wanted text second, so a path in the first argument
// and a slash literal in the second is the same assertion about the separator.
var stringFuncs = map[string]bool{
	"Contains":  true,
	"HasPrefix": true,
	"HasSuffix": true,
	"EqualFold": true,
}

// slashLiteralsComparedToAPath returns the slash literals in cond that are
// compared against a path-valued expression.
func slashLiteralsComparedToAPath(cond ast.Expr, paths map[string]bool) []string {
	var found []string
	ast.Inspect(cond, func(n ast.Node) bool {
		switch e := n.(type) {
		case *ast.BinaryExpr:
			if e.Op != token.EQL && e.Op != token.NEQ {
				return true
			}
			for _, pair := range [][2]ast.Expr{{e.X, e.Y}, {e.Y, e.X}} {
				if !isPathValued(pair[0], paths) {
					continue
				}
				if lit := slashLiteral(pair[1]); lit != "" {
					found = append(found, lit)
				}
			}
		case *ast.CallExpr:
			sel, ok := e.Fun.(*ast.SelectorExpr)
			if !ok || len(e.Args) != 2 {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "strings" || !stringFuncs[sel.Sel.Name] {
				return true
			}
			if !isPathValued(e.Args[0], paths) {
				return true
			}
			if lit := slashLiteral(e.Args[1]); lit != "" {
				found = append(found, lit)
			}
		}
		return true
	})
	return found
}

// reportsWithErrorf reports whether the block calls t.Errorf or t.Fatalf: the
// rule reads assertions, not ordinary control flow over a path.
func reportsWithErrorf(body *ast.BlockStmt) bool {
	reports := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name == "Errorf" || sel.Sel.Name == "Fatalf" {
			reports = true
		}
		return true
	})
	return reports
}

// TestPathAssertHeuristicReadsWhatItClaims is the class test's own test. A class
// test that finds nothing is indistinguishable from one that cannot find
// anything, and the tree is clean today — so the shapes the rule claims to catch,
// and the ones it claims to leave alone, are fed through it here as source text.
// A future narrowing that quietly blinds the rule is a red run in this function.
func TestPathAssertHeuristicReadsWhatItClaims(t *testing.T) {
	cases := []struct {
		name string
		want int // how many literals the heuristic must flag
		src  string
	}{
		{name: "path var against a slash literal", want: 1, src: `
			got := filepath.Join(dir, "a", "b")
			if got != "want/a/b" {
				t.Errorf("got %q", got)
			}`},
		{name: "the call compared directly", want: 1, src: `
			if filepath.Dir(p) != "root/sub" {
				t.Fatalf("bad dir")
			}`},
		{name: "t.TempDir is a host path", want: 1, src: `
			dir := t.TempDir()
			if dir == "x/y" {
				t.Errorf("no")
			}`},
		{name: "strings.HasPrefix on a path", want: 1, src: `
			got := filepath.Abs(p)
			if !strings.HasPrefix(got, "cmd/nova-bus") {
				t.Errorf("no")
			}`},
		{name: "ToSlash is the portable form", want: 0, src: `
			got := filepath.Join(dir, "a", "b")
			if filepath.ToSlash(got) != "want/a/b" {
				t.Errorf("got %q", got)
			}`},
		{name: "a want side built with Join", want: 0, src: `
			got := filepath.Join(dir, "a")
			if got != filepath.Join(dir, "want", "a") {
				t.Errorf("got %q", got)
			}`},
		{name: "an ordinary string is not a path", want: 0, src: `
			line := readLine()
			if line != "note/one two" {
				t.Errorf("got %q", line)
			}`},
		{name: "a URL is not a path", want: 0, src: `
			got := filepath.Join(dir, "a")
			if got != "https://example.com/a" {
				t.Errorf("got %q", got)
			}`},
		{name: "control flow is not an assertion", want: 0, src: `
			got := filepath.Join(dir, "a")
			if got != "a/b" {
				return
			}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package p\nfunc f(t *testing.T) {\n" + tc.src + "\n}\n"
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "heuristic.go", src, 0)
			if err != nil {
				t.Fatalf("cannot parse the case: %v", err)
			}
			fn := file.Decls[0].(*ast.FuncDecl)
			got := flaggedPathAssertions(fn)
			if len(got) != tc.want {
				t.Errorf("flagged %d assertion(s) %v, want %d", len(got), got, tc.want)
			}
		})
	}
}

func readPathAssertAllowlist(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(pathAssertAllowlistPath)
	if err != nil {
		t.Fatal(err)
	}
	allow := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		allow[line] = true
	}
	return allow
}
