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

// `nova-sprint card` read the work table whole three times (CardOf, Held and the place's
// Load) and the log once, about 100 server-ms at 2,000 cards; verbs ran 6 to 17 s under
// load. The card verb now asks the store for its whole view in one load (Store.CardRead,
// pinned on the twin by TestACardsViewReadsTheTablesOnceAndAnswersWhatThreeReadsDid).
// This holds the verb to it, read from the source: cmdCard makes the one store call that
// loads the tables and no other.
func TestNovaSprintCardReadsTheWorkTableOnce(t *testing.T) {
	t.Parallel()
	path := filepath.Join(repoRoot(t), "cmd/nova-sprint/reads.go")
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err)
	var fn *ast.FuncDecl
	for _, d := range f.Decls {
		if x, ok := d.(*ast.FuncDecl); ok && x.Name.Name == "cmdCard" {
			fn = x
		}
	}
	require.NotNil(t, fn, "cmdCard is in cmd/nova-sprint/reads.go")
	calls := map[string]int{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
				if recv, ok := sel.X.(*ast.Ident); ok && recv.Name == "st" {
					calls[sel.Sel.Name]++
				}
			}
		}
		return true
	})
	assert.Equal(t, 1, calls["CardRead"], "the card's records, what holds it and its place come from one CardRead")
	for _, name := range []string{"CardOf", "Held", "Load", "Records"} {
		assert.Zero(t, calls[name], "cmdCard calls st.%s: a second read of the work table", name)
	}
}
