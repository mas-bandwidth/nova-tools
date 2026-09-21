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
	var missing []string
	for _, tool := range tools {
		if hasOnboardingCall(t, root, tool) {
			continue
		}
		missing = append(missing, tool)
	}
	sort.Strings(missing)

	for _, m := range missing {
		t.Errorf("%s: section %q has no test calling onboarding.Compare or onboarding.Execute in cmd/%s/; every transcript section must be executed by a test using the shared comparator (docs/SPEC-TOOLWORK.md §3)",
			testsMDPath, m, m)
	}
}

func toolSectionsFromMD(md string) []string {
	var tools []string
	for _, line := range strings.Split(md, "\n") {
		rest, ok := strings.CutPrefix(line, "## ")
		if !ok {
			continue
		}
		name := strings.Fields(rest)[0]
		if strings.HasPrefix(name, "nova-") && name != "nova-" {
			tools = append(tools, name)
		}
	}
	return tools
}

func hasOnboardingCall(t *testing.T, root, tool string) bool {
	t.Helper()

	candidates := []string{
		filepath.Join(root, "cmd", tool, "firstrun_test.go"),
		filepath.Join(root, "cmd", tool, "hygiene_test.go"),
	}
	for _, path := range candidates {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, raw, 0)
		if err != nil {
			t.Errorf("cannot parse %s: %v", path, err)
			continue
		}
		found := false
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if x.Name == "onboarding" && (sel.Sel.Name == "Compare" || sel.Sel.Name == "Execute" || sel.Sel.Name == "ExecuteWith") {
				found = true
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}
