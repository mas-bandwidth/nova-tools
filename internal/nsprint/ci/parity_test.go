package ci_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// TestParityNeverPollsGitHub is THE BOUNDARY of #3041: the parity read is
// Redis alone (s:<S>, ev:github, ci:<repo>:<sha>). parity.go may not import an
// HTTP or process package, name the GitHub API, or call the one budgeted
// GitHub read (FetchCheckRuns, Compare in compare.go).
func TestParityNeverPollsGitHub(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "parity.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range file.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if strings.HasPrefix(path, "net") || path == "os/exec" || strings.Contains(path, "github.com/google/go-github") {
			t.Errorf("parity.go imports %s: the parity read never leaves Redis", path)
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if x.Name == "FetchCheckRuns" || x.Name == "Compare" {
				t.Errorf("%s: parity.go calls %s, the GitHub read", fset.Position(x.Pos()), x.Name)
			}
		case *ast.BasicLit:
			if strings.Contains(x.Value, "api.github.com") || strings.Contains(x.Value, "graphql") {
				t.Errorf("%s: parity.go names the GitHub API: %s", fset.Position(x.Pos()), x.Value)
			}
		}
		return true
	})
}
