package brief

import (
	"context"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Lint checks a brief file against its task record. First commit of
// nova-tools#3154 build a: the API only, accepting every brief, so the
// wrong-co-author fixture test is red on a clean tree before any check exists.
func Lint(ctx context.Context, st *store.Store, path, taskFlag string, stdout, stderr io.Writer) int {
	return 0
}
