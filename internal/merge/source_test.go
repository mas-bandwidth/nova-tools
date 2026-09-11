package merge

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
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
	// The writing sites this package is allowed, EACH ONE NAMED. It used to be a file
	// name with a sentence, so any number of opens in state.go, records.go or lock.go
	// passed on the strength of the first (read 4b, finding 6). Three sites is the whole
	// of it, and writeWhole is the only one that writes a record path -- which is what
	// makes the runtime tripwire over WatchWrites a tripwire on every path opened.
	allowed := map[string][]struct{ expr, what string }{
		"records.go": {{"return os.WriteFile(file, body, perm)", "writeWhole: the outbox item, the record path the CAS loop restores, and the state's temp name"}},
		"state.go":   {{"os.OpenFile(filepath.Join(lane, LogName)", "the lane's log, append-only and never rotated"}},
		"lock.go":    {{"os.OpenFile(path, os.O_RDWR|os.O_CREATE", "the lock file, whose content is the holder's pid for a waiter's refusal"}},
	}
	used := map[string]int{}
	for name, src := range packageSource(t) {
		for i, line := range strings.Split(src, "\n") {
			if !strings.Contains(line, "os.WriteFile") && !strings.Contains(line, "os.OpenFile") && !strings.Contains(line, "os.Create") {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			site := ""
			for _, s := range allowed[name] {
				if strings.Contains(line, s.expr) {
					site = s.expr
					break
				}
			}
			if site == "" {
				t.Errorf("%s:%d writes a file: %s\nthe lane never edits an entry's content, and a new writing site is a decision rather than a drive-by", name, i+1, strings.TrimSpace(line))
				continue
			}
			used[name+" "+site]++
		}
		// A mechanical resolver is what this spec withdrew: no conflict-marker parsing,
		// no diff3, no resolver of any kind.
		for _, forbidden := range []string{"<<<<<<<", "diff3", "resolve_mechanical", "--strategy-option"} {
			if strings.Contains(src, forbidden) {
				t.Errorf("%s carries %q; there is no mechanical resolve in this tool (rule 7, withdrawn 2026-09-11 after four real conflicts on a file no rule named)", name, forbidden)
			}
		}
	}
	for name, sites := range allowed {
		for _, s := range sites {
			if n := used[name+" "+s.expr]; n != 1 {
				t.Errorf("the allowance for %q in %s matched %d writing sites, want exactly one (%s)", s.expr, name, n, s.what)
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

// Rule 4: THE GUARD IS IN FRONT OF EVERY COMMAND THIS TOOL STARTS.
//
// gitops.go used to claim the guard is on run() "because the one function that runs a
// mutating command is only true if there is no second function that runs anything" -- and
// there is a second: GH.gh reaches Runner.Run directly. It guards first, so rule 4 held,
// but nothing said so, and guard_test.go drives Git.Run alone. A third call site would be
// an unguarded mutation path with nothing red. The prototype's guard lived in a shell
// function called `mut`, and every other call site in the file ran git or gh directly.
//
// So: every function that reaches Runner.Run calls guard() first, and this reads the
// positions rather than trusting the order a reader remembers.
func TestEveryRunnerCallSiteIsGuardedFirst(t *testing.T) {
	fset := token.NewFileSet()
	sites := 0
	for name := range packageSource(t) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			runner, guarded := token.NoPos, token.NoPos
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.SelectorExpr:
					// <anything>.Runner.Run(...)
					if fun.Sel.Name != "Run" {
						return true
					}
					inner, ok := fun.X.(*ast.SelectorExpr)
					if !ok || inner.Sel.Name != "Runner" {
						return true
					}
					if runner == token.NoPos {
						runner = call.Pos()
					}
				case *ast.Ident:
					if fun.Name == "guard" && guarded == token.NoPos {
						guarded = call.Pos()
					}
				}
				return true
			})
			if runner == token.NoPos {
				continue
			}
			sites++
			switch {
			case guarded == token.NoPos:
				t.Errorf("%s's %s hands a command to a Runner and never calls guard; rule 4's four spellings are refused in the one function that runs a command, BEFORE the command is built",
					name, fn.Name.Name)
			case guarded > runner:
				t.Errorf("%s's %s calls guard at line %d and runs the command at line %d; the guard runs BEFORE the command is built, not after",
					name, fn.Name.Name, fset.Position(guarded).Line, fset.Position(runner).Line)
			}
		}
	}
	// Two today: Git.Run and GH.gh. A zero here is a tripwire that has stopped looking.
	if sites < 2 {
		t.Fatalf("this tripwire found %d call sites reaching a Runner; it was looking in the wrong place and would have passed by checking nothing", sites)
	}
}

// A commit object this tool writes carries nova-merge's own identity, on the command.
//
// The Ubuntu leg of #57 went red on 2026-09-11 with "Committer identity unknown" from
// `git merge --no-ff`: the two commit sites carried `-c user.name=... -c user.email=...`
// and the two merge sites did not, so the lane worked on every machine whose git could
// guess a name from the account and died on a runner whose checkout has no gitconfig at
// all. The identity is now one helper, and this reads the source so a third writing site
// added later is red here rather than on one operating system.
func TestEveryCommitWritingCommandCarriesTheIdentity(t *testing.T) {
	fset := token.NewFileSet()
	checked := 0
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
			if !ok || (sel.Sel.Name != "Run" && sel.Sel.Name != "Out") {
				return true
			}
			args := call.Args
			identity := false
			if len(args) == 1 {
				if inner, ok := args[0].(*ast.CallExpr); ok {
					if id, ok := inner.Fun.(*ast.Ident); ok && id.Name == "Identity" {
						identity, args = true, inner.Args
					}
				}
			}
			words := literals(args)
			if len(words) == 0 || !writesACommit(words) {
				return true
			}
			checked++
			if !identity {
				t.Errorf("%s:%d runs `git %s` without merge.Identity; a machine with no git identity -- every CI runner -- answers \"Committer identity unknown\" and the pass dies after the entry is already checked out",
					name, fset.Position(call.Pos()).Line, strings.Join(words, " "))
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no commit-writing git command found in this package; this test was looking for the wrong shape and would have passed by checking nothing")
	}
}

// literals returns the leading string literals of an argument list, which is how every
// git command in this package starts.
func literals(args []ast.Expr) []string {
	out := []string(nil)
	for _, a := range args {
		lit, ok := a.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			break
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			break
		}
		out = append(out, value)
	}
	return out
}

// writesACommit reports whether the command makes a commit object. `merge --abort` and
// `merge --ff-only` move a ref and write none.
func writesACommit(words []string) bool {
	switch words[0] {
	case "commit":
		return true
	case "merge":
		for _, w := range words[1:] {
			if w == "--abort" || w == "--ff-only" {
				return false
			}
		}
		return true
	}
	return false
}

// The lane's .gitignore names no sentinel lock file.
//
// Rule 1, docs/SPEC-MERGE.md: "One state file, one lock. ... an OS lock the kernel
// releases on death ... never a sentinel file or a directory". The Windows fix deleted the
// sentinel writer, and the pattern for the file it used to leave behind stayed in the
// ignore list -- a rule that reads as though a sentinel is still expected.
func TestTheLanesIgnoreListNamesNoSentinel(t *testing.T) {
	for _, leftover := range []string{".held", ".lock.d", ".lockdir"} {
		if strings.Contains(GitIgnore, leftover) {
			t.Errorf("the lane's .gitignore names %q; the lock is an OS lock the kernel releases on death, never a sentinel file or a directory (rule 1)", leftover)
		}
	}
	// The lock files git and this tool really write are still ignored, so a lane
	// mid-verb is clean.
	if !strings.Contains(GitIgnore, "*.lock") {
		t.Error("the lane's .gitignore no longer ignores *.lock; a git status in a lane that is mid-verb would not be clean")
	}
}
