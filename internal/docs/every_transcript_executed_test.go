package docs

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const testsMDPath = "../../docs/TESTS.md"

// notYetExecuted is the shrink-only allowlist docs/SPEC-TOOLWORK.md §7 rule 4
// asks for: the docs/TESTS.md sections no firstrun_test.go executes yet, each
// with the issue that owes it. It shrinks in both directions: a listed section
// that gains a test, or that is no longer a section, fails until its line is
// removed. Nothing may be added to it.
var notYetExecuted = map[string]string{
	"nova-bus":     "#1652 (T7): firstrun_test.go reads the section by hand, not through onboarding.Execute",
	"nova-play":    "#1652 (T7): firstrun_test.go reads the section by hand, not through onboarding.Execute",
	"nova-sandbox": "#1652 (T7): firstrun_test.go checks field names, not the transcript line for line",
	"nova-secrets": "#1652 (T7): no firstrun_test.go",
	"nova-sprint":  "#1652 (T7): firstrun_test.go reads the section by hand, not through onboarding.Execute",
}

func TestEveryTranscriptIsExecutedLineForLine(t *testing.T) {
	t.Parallel()

	md, err := os.ReadFile(testsMDPath)
	if err != nil {
		t.Fatalf("reading %s: %v", testsMDPath, err)
	}

	tools := toolSectionsFromMD(string(md))
	if len(tools) == 0 {
		t.Fatal("no tool sections found in docs/TESTS.md")
	}

	root := filepath.Join("..", "..")
	sections := map[string]bool{}
	var missing []string
	for _, tool := range tools {
		sections[tool] = true
		executed := firstRunExecutes(t, root, tool)
		owed, listed := notYetExecuted[tool]
		switch {
		case executed && listed:
			t.Errorf("section %q is now executed by cmd/%s/firstrun_test.go; remove it from notYetExecuted (was owed by %s)", tool, tool, owed)
		case !executed && !listed:
			missing = append(missing, tool)
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("%s: section %q has no test in cmd/%s/firstrun_test.go calling onboarding.CompareTranscript, onboarding.Compare or onboarding.Execute; every transcript section must be executed by a test using the shared comparator (docs/SPEC-TOOLWORK.md §3), and notYetExecuted only shrinks",
			testsMDPath, m, m)
	}

	var stale []string
	for tool := range notYetExecuted {
		if !sections[tool] {
			stale = append(stale, tool)
		}
	}
	sort.Strings(stale)
	for _, s := range stale {
		t.Errorf("notYetExecuted lists %q, which is not a section of %s; remove it", s, testsMDPath)
	}
}

func toolSectionsFromMD(md string) []string {
	var tools []string
	for _, line := range strings.Split(md, "\n") {
		rest, ok := strings.CutPrefix(line, "## ")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		if strings.HasPrefix(name, "nova-") && name != "nova-" {
			tools = append(tools, name)
		}
	}
	return tools
}

// firstRunExecutes reports whether cmd/<tool>/firstrun_test.go calls
// onboarding.CompareTranscript, Compare, Execute or ExecuteWith from a Test function, directly
// or through a helper or function literal declared in the same file. Each
// function literal is its own node in the call graph and is reached only
// when it is invoked: called in place (func(){...}(), go and defer
// included), called through the local name it was assigned to, or handed to
// t.Run or t.Cleanup. A call in a function or literal no test reaches does
// not count, so an uninvoked closure inside a Test is dead code here.
func firstRunExecutes(t *testing.T, root, tool string) bool {
	t.Helper()

	path := filepath.Join(root, "cmd", tool, "firstrun_test.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	f, err := parser.ParseFile(token.NewFileSet(), path, raw, 0)
	if err != nil {
		t.Errorf("cannot parse %s: %v", path, err)
		return false
	}

	calls := map[string][]string{} // node -> same-file nodes it invokes
	direct := map[string]bool{}    // node -> calls the comparator itself
	lits := map[*ast.FuncLit]string{}
	litNode := func(lit *ast.FuncLit) string {
		if id, ok := lits[lit]; ok {
			return id
		}
		id := fmt.Sprintf("func-literal#%d", len(lits))
		lits[lit] = id
		return id
	}

	// walk records the edges of one node's body. It stops at every function
	// literal and walks that literal's body as a node of its own, so nothing
	// inside a closure is credited to the function that merely declares it.
	// locals maps a name assigned a literal to that literal's node; it is
	// shared by one declaration and every literal nested inside it.
	var walk func(node string, body ast.Node, locals map[string]string)
	walk = func(node string, body ast.Node, locals map[string]string) {
		ast.Inspect(body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncLit:
				walk(litNode(n), n.Body, locals)
				return false
			case *ast.AssignStmt:
				if len(n.Lhs) == len(n.Rhs) {
					for i, rhs := range n.Rhs {
						lit, ok := rhs.(*ast.FuncLit)
						id, isIdent := n.Lhs[i].(*ast.Ident)
						if ok && isIdent {
							locals[id.Name] = litNode(lit)
						}
					}
				}
			case *ast.ValueSpec:
				if len(n.Names) == len(n.Values) {
					for i, v := range n.Values {
						if lit, ok := v.(*ast.FuncLit); ok {
							locals[n.Names[i].Name] = litNode(lit)
						}
					}
				}
			case *ast.CallExpr:
				switch fun := n.Fun.(type) {
				case *ast.FuncLit:
					calls[node] = append(calls[node], litNode(fun))
				case *ast.Ident:
					if lit, ok := locals[fun.Name]; ok {
						calls[node] = append(calls[node], lit)
					} else {
						calls[node] = append(calls[node], fun.Name)
					}
				case *ast.SelectorExpr:
					if x, ok := fun.X.(*ast.Ident); ok && x.Name == "onboarding" {
						switch fun.Sel.Name {
						case "Compare", "CompareTranscript", "Execute", "ExecuteWith":
							direct[node] = true
						}
					}
					if fun.Sel.Name == "Run" || fun.Sel.Name == "Cleanup" {
						for _, arg := range n.Args {
							switch arg := arg.(type) {
							case *ast.FuncLit:
								calls[node] = append(calls[node], litNode(arg))
							case *ast.Ident:
								if lit, ok := locals[arg.Name]; ok {
									calls[node] = append(calls[node], lit)
								} else {
									calls[node] = append(calls[node], arg.Name)
								}
							}
						}
					}
				}
			}
			return true
		})
	}

	var tests []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Recv != nil {
			continue
		}
		name := fn.Name.Name
		if strings.HasPrefix(name, "Test") {
			tests = append(tests, name)
		}
		walk(name, fn.Body, map[string]string{})
	}

	seen := map[string]bool{}
	var reaches func(string) bool
	reaches = func(name string) bool {
		if seen[name] {
			return false
		}
		seen[name] = true
		if direct[name] {
			return true
		}
		for _, callee := range calls[name] {
			if reaches(callee) {
				return true
			}
		}
		return false
	}
	for _, name := range tests {
		if reaches(name) {
			return true
		}
	}
	return false
}

// TestFirstRunExecutesCountsOnlyInvokedCalls is the control for
// firstRunExecutes: each case is a firstrun_test.go whose only comparator call
// sits in one place, and the check must credit the calls a Test reaches and
// refuse the dead ones (an uncalled helper, a closure that is declared or
// assigned but never invoked).
func TestFirstRunExecutesCountsOnlyInvokedCalls(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want bool
	}{
		{"direct call in a Test", `func TestX(t *testing.T) { onboarding.Execute(nil, nil) }`, true},
		{"helper a Test calls", `func TestX(t *testing.T) { run() }
func run() { onboarding.Compare(nil, nil) }`, true},
		{"helper no Test calls", `func TestX(t *testing.T) {}
func run() { onboarding.Compare(nil, nil) }`, false},
		{"uninvoked closure in a Test", `func TestX(t *testing.T) { _ = func() { onboarding.Execute(nil, nil) } }`, false},
		{"closure assigned to a local and never called", `func TestX(t *testing.T) {
	run := func() { onboarding.Execute(nil, nil) }
	_ = run
}`, false},
		{"closure returned by a helper nobody invokes", `func TestX(t *testing.T) { _ = runner() }
func runner() func() { return func() { onboarding.Execute(nil, nil) } }`, false},
		{"closure called in place", `func TestX(t *testing.T) { func() { onboarding.Execute(nil, nil) }() }`, true},
		{"closure deferred", `func TestX(t *testing.T) { defer func() { onboarding.Execute(nil, nil) }() }`, true},
		{"closure called through its local name", `func TestX(t *testing.T) {
	run := func() { onboarding.Execute(nil, nil) }
	run()
}`, true},
		{"closure var called through its local name", `func TestX(t *testing.T) {
	var run = func() { onboarding.ExecuteWith(nil, nil) }
	run()
}`, true},
		{"closure handed to t.Run", `func TestX(t *testing.T) { t.Run("x", func(t *testing.T) { onboarding.Execute(nil, nil) }) }`, true},
		{"nested closure only the outer one invokes", `func TestX(t *testing.T) {
	outer := func() {
		_ = func() { onboarding.Execute(nil, nil) }
	}
	outer()
}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, "cmd", "nova-x")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			src := "package main\n\nimport (\n\t\"testing\"\n\n\t\"example.invalid/onboarding\"\n)\n\n" + tc.body + "\n"
			if err := os.WriteFile(filepath.Join(dir, "firstrun_test.go"), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := firstRunExecutes(t, root, "nova-x"); got != tc.want {
				t.Errorf("firstRunExecutes = %v, want %v for:\n%s", got, tc.want, tc.body)
			}
		})
	}
}
