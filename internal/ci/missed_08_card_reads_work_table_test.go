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

// TestNovaSprintCardReadsTheWorkTableOnce pins that cmdCard takes its record, what holds
// it and its place in line from the one read store.CardOfHeld makes, and never reads the
// tables again itself (Load, Held or CardOf): `nova-sprint card` read the work table
// whole three times, about 100 server-ms at 2,000 cards. The count of whole reads is
// TestACardIsOneWholeRead (internal/sprint/store).
func TestNovaSprintCardReadsTheWorkTableOnce(t *testing.T) {
	t.Parallel()
	path := filepath.Join(repoRoot(t), "cmd", "nova-sprint", "reads.go")
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err)
	var fn *ast.FuncDecl
	for _, d := range f.Decls {
		if x, ok := d.(*ast.FuncDecl); ok && x.Name.Name == "cmdCard" {
			fn = x
		}
	}
	require.NotNil(t, fn, "cmdCard")
	calls := map[string]int{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
				calls[sel.Sel.Name]++
			}
		}
		return true
	})
	assert.Zero(t, calls["Load"], "cmdCard reads the tables through CardOfHeld, not Load")
	assert.Zero(t, calls["Held"], "cmdCard takes what holds the card from CardOfHeld, not Held")
	assert.Equal(t, 1, calls["CardOfHeld"], "the full card is one CardOfHeld")
}
