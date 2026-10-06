package ci

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNovaSprintCardReadsTheWorkTableOnce pins that `nova-sprint card` reads the tables
// whole once, through store.CardOfHeld: cmdCard asks the store for neither a Load of the
// work table nor Held, each of which is another whole read of it.
func TestNovaSprintCardReadsTheWorkTableOnce(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(repoRoot(t), "cmd", "nova-sprint", "reads.go"), nil, 0)
	require.NoError(t, err)
	var body *ast.BlockStmt
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "cmdCard" {
			body = fn.Body
		}
	}
	require.NotNil(t, body, "cmdCard is not in cmd/nova-sprint/reads.go")
	calls := map[string]int{}
	ast.Inspect(body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
				calls[sel.Sel.Name]++
			}
		}
		return true
	})
	assert.Zero(t, calls["Load"], "cmdCard reads the work table whole again: take it from CardOfHeld's Table")
	assert.Zero(t, calls["Held"], "cmdCard reads the tables whole again for what holds the card: take it from CardOfHeld's Held")
	assert.Positive(t, calls["CardOfHeld"], "cmdCard does not read the card through CardOfHeld")
}
