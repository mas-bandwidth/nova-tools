package pulse

// THE CLASS TEST FOR THE BRANCH RULE (Stella's ruling on nova-tools #1824; Johnny's hold on
// PR #1809).
//
// The first cut of the rule lived on the local `Harvest()` path alone. `harvest --working`,
// `harvest --bench` and the manager each have their own push, each reached that push by its
// own route, and each pushed a branch the rule would have refused. A rule implemented once
// per caller is not one rule; it is as many rules as there are callers, and they drift the
// moment one of them is edited.
//
// So this test does not test a behaviour. It enumerates, FROM THE PACKAGE'S OWN SOURCE,
// every function that runs `git push` or opens a pull request, and requires each one to call
// `mustBranchPrefix`. A fifth push site added tomorrow fails this test on the commit that
// adds it, which is the only moment the omission is cheap to see. The shape is the secret
// scan's: read the tree, list the offenders, name each one with its file and line.

import (
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

// theGuard is the one implementation every push path must call.
const theGuard = "mustBranchPrefix"

func TestEveryPushPathChecksTheBranchPrefix(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	type site struct{ where, what string }
	var unguarded []site
	guarded := 0
	seen := map[string]bool{} // every function this detector recognises as a push path

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			what, pos := pushSiteIn(fn)
			if what == "" {
				continue
			}
			seen[fn.Name.Name] = true
			if callsGuard(fn) {
				guarded++
				continue
			}
			unguarded = append(unguarded, site{
				where: filepath.Join("internal/pulse", name) + ":" + strconv.Itoa(fset.Position(pos).Line),
				what:  fn.Name.Name + " -> " + what,
			})
		}
	}

	// THE DETECTOR IS PART OF THE TEST. A class test that has quietly stopped seeing its
	// class passes forever and protects nothing, so the four push paths this package is
	// known to have are named here by function, and a detector that misses one fails.
	for _, want := range []string{
		"push",         // harvest.go, the local fold
		"openPR",       // harvest.go (gh pr create) and manager.go (git push), the pull request
		"one",          // harvest_working.go, the --working fold (force-with-lease)
		"harvestBench", // harvestbench.go, the --bench fold
	} {
		if !seen[want] {
			t.Errorf("the detector no longer sees %s() as a push path; it is one, and this test is now blind to it", want)
		}
	}
	if guarded == 0 {
		t.Fatalf("this test found no guarded push site at all; the detector has stopped seeing them, which makes it worse than no test")
	}
	t.Logf("PUSH-PATH OK sites=%d guarded=%d unguarded=%d", guarded+len(unguarded), guarded, len(unguarded))
	if len(unguarded) > 0 {
		sort.Slice(unguarded, func(i, j int) bool { return unguarded[i].where < unguarded[j].where })
		for _, u := range unguarded {
			t.Errorf("PUSH-PATH %s %s: this pushes or opens a PR without calling %s (Stella's ruling on #1824: every branch this line pushes is under %s, and the prefix is not configurable)",
				u.where, u.what, theGuard, DefaultBranchPrefix)
		}
		t.Errorf("PUSH-PATH FAIL guarded=%d unguarded=%d", guarded, len(unguarded))
	}
}

// pushSiteIn reports what this function does that reaches a remote, and where: a `git push`
// in any of the three spellings this package uses to run a child, or a pull request opened
// through the Forge. It returns "" for a function that does neither.
func pushSiteIn(fn *ast.FuncDecl) (string, token.Pos) {
	var what string
	var pos token.Pos
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "CreatePR" {
			what, pos = "CreatePR", call.Pos()
			return false
		}
		// A `git push` is the literal "push" in the argument list of a call that also
		// names git -- either as the program ("git", "push", ...) or by running in a
		// clone (gitIn(clone, "push", ...)). Both spellings are in this package.
		args := literalArgs(call)
		if !args["push"] {
			return true
		}
		if args["git"] || isGitHelper(call.Fun) {
			what, pos = "git push", call.Pos()
			return false
		}
		return true
	})
	if what == "" {
		// A pull request opened by running gh directly, which is how the local fold
		// does it: `gh pr create`. It reaches the same remote as a push and takes the
		// same rule, and the first version of this detector walked straight past it.
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			args := literalArgs(call)
			if args["gh"] && args["pr"] && args["create"] {
				what, pos = "gh pr create", call.Pos()
				return false
			}
			return true
		})
	}
	return what, pos
}

// literalArgs is the set of string literals a call passes, at the top level of its argument
// list. It is deliberately shallow: a push assembled out of variables so that none of its
// words appear here would defeat it, and the honest answer to that is that nothing in this
// package does, and this test's own failure message says what it looks for.
func literalArgs(call *ast.CallExpr) map[string]bool {
	out := map[string]bool{}
	for _, a := range call.Args {
		lit, ok := a.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		if v, err := strconv.Unquote(lit.Value); err == nil {
			out[v] = true
		}
	}
	return out
}

// isGitHelper reports whether the function being called is one of this package's own
// wrappers around git.
func isGitHelper(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name == "gitIn" || f.Name == "runChild"
	case *ast.SelectorExpr:
		return f.Sel.Name == "gitIn" || f.Sel.Name == "runChild" || f.Sel.Name == "sh"
	}
	return false
}

func callsGuard(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == theGuard {
			found = true
			return false
		}
		return true
	})
	return found
}
