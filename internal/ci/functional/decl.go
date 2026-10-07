package functional

import (
	"go/ast"
	"strings"
)

// topLevelTest reports a func declaration go test runs as a test: no receiver,
// a Test prefix, and not TestMain.
func topLevelTest(decl ast.Decl) (string, bool) {
	fn, ok := decl.(*ast.FuncDecl)
	if !ok || fn.Recv != nil {
		return "", false
	}
	name := fn.Name.Name
	if !strings.HasPrefix(name, "Test") || name == "TestMain" {
		return "", false
	}
	return name, true
}
