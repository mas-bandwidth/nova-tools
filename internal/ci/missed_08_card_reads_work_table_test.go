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

// TestCardReadsTheWorkTableOnce pins `nova-sprint card` to one read of the
// tables: cmdCard takes the card, what holds it and its place in line from the
// store's one CardHeld read (CardOf at an earlier epoch, which tells no hold), and never loads the tables itself (store.Load) or
// asks Held, each of which reads the work table whole again.
func TestCardReadsTheWorkTableOnce(t *testing.T) {
	t.Parallel()

	path := filepath.Join(repoRoot(t), "cmd", "nova-sprint", "reads.go")
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err)
	var body *ast.BlockStmt
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "cmdCard" {
			body = fn.Body
		}
	}
	require.NotNil(t, body, "reads.go declares cmdCard")
	calls := map[string]int{}
	ast.Inspect(body, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			calls[sel.Sel.Name]++
		}
		return true
	})
	assert.Zero(t, calls["Load"], "cmdCard reads the work table through CardHeld, not a Load of its own")
	assert.Zero(t, calls["Held"], "cmdCard takes what holds the card from CardHeld, not a Held read of the tables")
	assert.Equal(t, 1, calls["CardOf"], "cmdCard reads an earlier epoch's card, which tells no hold, with CardOf alone")
	assert.Equal(t, 1, calls["CardHeld"], "cmdCard reads the card, its hold and its place with one CardHeld")
}
