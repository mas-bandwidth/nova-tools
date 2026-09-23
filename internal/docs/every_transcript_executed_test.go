package docs

import (
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
	"nova-post":    "#1631: the section is documented as not runnable until posting credentials resolve; TestTESTSFirstRunIsNotRunnableUntil1631 pins that",
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
		t.Errorf("%s: section %q has no test in cmd/%s/firstrun_test.go calling onboarding.Compare or onboarding.Execute; every transcript section must be executed by a test using the shared comparator (docs/SPEC-TOOLWORK.md §3), and notYetExecuted only shrinks",
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
// onboarding.Compare, Execute or ExecuteWith from a Test function, directly
// or through a helper declared in the same file. A call in a function no
// test reaches does not count.
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

	calls := map[string][]string{} // func name -> same-file funcs it calls
	direct := map[string]bool{}    // func name -> calls the comparator itself
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
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				calls[name] = append(calls[name], fun.Name)
			case *ast.SelectorExpr:
				if x, ok := fun.X.(*ast.Ident); ok && x.Name == "onboarding" {
					switch fun.Sel.Name {
					case "Compare", "Execute", "ExecuteWith":
						direct[name] = true
					}
				}
			}
			return true
		})
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
