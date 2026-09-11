package merge

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The source-level tripwires. Three of this tool's rules are properties of the CODE and
// are not provable from any output, so they are asserted over the source: nothing writes
// into the clone's work tree (rule 7), nothing reaches /tmp or the process table (rule
// 13), and the publication helper has exactly one call site (rules 18 and 21).
//
// Reaching outside t.TempDir() has one reason here and it is stated: the subject of these
// tests IS the source.

func packageSource(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(".", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(raw)
	}
	if len(out) == 0 {
		t.Fatal("no source files found; this test was looking in the wrong place and would have passed by checking nothing")
	}
	return out
}

// Rule 7: THERE IS NO CODE PATH IN nova-merge THAT WRITES A RESOLVED FILE. The lane never
// edits an entry's content, so nothing in this package opens a file for writing anywhere
// but the lane's own state, log, outbox and record paths -- and git, not this code, is
// what writes into a work tree.
func TestNothingWritesIntoTheClonesWorkTree(t *testing.T) {
	// The writing sites this package is allowed, each with the thing it writes.
	allowed := map[string]string{
		"state.go":      "the lane's own state file, through the fixed temp name and a rename",
		"records.go":    "the outbox, and the record path the CAS loop restores from the outbox",
		"lock.go":       "the lock file, whose content is the holder's pid for a waiter's refusal",
		"lock_other.go": "the sentinel beside the lock file, where there is no flock (Windows)",
	}
	for name, src := range packageSource(t) {
		for i, line := range strings.Split(src, "\n") {
			if !strings.Contains(line, "os.WriteFile") && !strings.Contains(line, "os.OpenFile") && !strings.Contains(line, "os.Create") {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if _, ok := allowed[name]; !ok {
				t.Errorf("%s:%d writes a file: %s\nthe lane never edits an entry's content, and a new writing site is a decision rather than a drive-by", name, i+1, strings.TrimSpace(line))
			}
		}
		// A mechanical resolver is what this spec withdrew: no conflict-marker parsing,
		// no diff3, no resolver of any kind.
		for _, forbidden := range []string{"<<<<<<<", "diff3", "resolve_mechanical", "--strategy-option"} {
			if strings.Contains(src, forbidden) {
				t.Errorf("%s carries %q; there is no mechanical resolve in this tool (rule 7, withdrawn 2026-09-11 after four real conflicts on a file no rule named)", name, forbidden)
			}
		}
	}
}

// Rule 13: nothing under /tmp, and the tool never matches a process by its own command
// line. The state, the clone and the gate summaries live only under paths given by flags.
func TestNothingReachesTmpOrTheProcessTable(t *testing.T) {
	for name, src := range packageSource(t) {
		for _, forbidden := range []string{`"/tmp`, "os.TempDir", "pgrep", `"ps"`, "/proc/", "TMPDIR"} {
			if strings.Contains(src, forbidden) {
				t.Errorf("%s carries %q; every path this tool writes is under --lane, and a loop that matches a process by its own command line matches itself", name, forbidden)
			}
		}
	}
}

// Rules 18 and 21: the publication helper has ONE call site, and it is inside the
// function that evaluates the predicate. A second call site is a second way to publish,
// which is the shape the prototype had when a hand-typed `gh pr merge --auto` reached
// past its guard.
func TestThePublicationHelperHasOneCallSiteInsideThePredicate(t *testing.T) {
	fset := token.NewFileSet()
	sites := map[string]int{}
	for name := range packageSource(t) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Publish" {
				return true
			}
			sites[name+":"+enclosing(f, fset, call.Pos())]++
			return true
		})
	}
	if len(sites) != 1 {
		t.Fatalf("the publication helper is called from %v; exactly one call site, inside the predicate's own function, is the rule", sites)
	}
	for site, n := range sites {
		if n != 1 {
			t.Errorf("%s calls Publish %d times, want 1", site, n)
		}
		if !strings.HasSuffix(site, ":merge") {
			t.Errorf("the one call site is %s; it must be the function that evaluates MERGE(entry)", site)
		}
	}
}

func enclosing(f *ast.File, fset *token.FileSet, pos token.Pos) string {
	name := "?"
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Pos() <= pos && pos <= fn.End() {
			name = fn.Name.Name
		}
	}
	return name
}

// dry-run's fold and its plan cannot reach the mutating helper AT ALL, which is a
// property of the call graph and never of a flag. The survey calls plan, which calls
// neither build nor merge nor Publish nor Ready.
func TestTheSurveysCallGraphCannotReachAMutation(t *testing.T) {
	fset := token.NewFileSet()
	forbidden := map[string]bool{"Publish": true, "merge": true, "build": true, "remerge": true, "Ready": true, "Deliver": true, "SaveTo": true, "Update": true}
	for name := range packageSource(t) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || (fn.Name.Name != "plan" && fn.Name.Name != "survey" && fn.Name.Name != "Packet" && fn.Name.Name != "standing") {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				var called string
				switch fun := call.Fun.(type) {
				case *ast.SelectorExpr:
					called = fun.Sel.Name
				case *ast.Ident:
					called = fun.Name
				}
				if forbidden[called] {
					t.Errorf("%s's %s calls %s; nothing in the survey's code path may reach a mutation", name, fn.Name.Name, called)
				}
				return true
			})
		}
	}
}

// Rule 21 and demanded test 21: NO `gh pr merge` CALL WITHOUT A BASE PRECONDITION.
//
// `gh pr merge` takes --match-head-commit and nothing about the base, and a merge with
// only a head precondition is not the atomic publication rule 21 asks for. So: a gh
// invocation whose first two arguments are "pr" and "merge" must also carry a flag that
// names the expected BASE. On the host that offers no such primitive there is no such
// call at all, which is what keeps gh pr merge out of the merge path entirely -- the
// shape the prototype had when a hand-typed `gh pr merge --auto` reached past its guard.
func TestNoGhPrMergeCallLacksABasePrecondition(t *testing.T) {
	base := map[string]bool{"--match-base": true, "--match-base-commit": true, "--expected-base": true}
	fset := token.NewFileSet()
	calls := 0
	for name := range packageSource(t) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			args := stringArgs(call)
			if len(args) < 2 || args[0] != "pr" || args[1] != "merge" {
				return true
			}
			calls++
			carries := false
			for _, a := range args {
				if base[a] || base[strings.SplitN(a, "=", 2)[0]] {
					carries = true
				}
			}
			if !carries {
				t.Errorf("%s:%d builds a `gh pr merge` call with no base precondition: %v\nrule 21 publishes onto a base that is exactly the expected sha, and --match-head-commit is a precondition on the HEAD",
					name, fset.Position(call.Pos()).Line, args)
			}
			return true
		})
	}
	if calls != 0 {
		t.Logf("this package builds %d `gh pr merge` call(s); on a host that offers no two-precondition primitive there must be none at all", calls)
	}
}

// stringArgs is the string literals a call was given, in order, which is how a source test
// reads an argument list a function will hand a subprocess.
func stringArgs(call *ast.CallExpr) []string {
	var out []string
	for _, a := range call.Args {
		lit, ok := a.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			out = append(out, "")
			continue
		}
		out = append(out, strings.Trim(lit.Value, `"`))
	}
	return out
}
