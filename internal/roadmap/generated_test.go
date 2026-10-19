package roadmap

import (
	"os"
	"path/filepath"
	"testing"

	roadmapdoc "github.com/mas-bandwidth/nova-tools/pkg/roadmap"
	"github.com/stretchr/testify/require"
)

// checkGenerated holds the committed markdown page to the committed sexp it is generated
// from: decode the sexp through nova-tools pkg/roadmap, render it in process exactly as
// nova-tools' tools/roadmap does (`make roadmap`, `go tool roadmap`), and require the page
// to be those bytes. A hand edit of the page, or a sexp edit without regenerating, is red,
// and the failure names the command that regenerates it.
func checkGenerated[T any](t *testing.T, source, page string, decode func(string, []byte) (*T, error), render func(*T, string) string) {
	t.Helper()
	root := filepath.Join("..", "..")
	raw, err := os.ReadFile(filepath.Join(root, source))
	require.NoError(t, err, "reading %s: %v", source, err)
	doc, err := decode(source, raw)
	require.NoError(t, err, "%s does not decode; fix it, then run `make roadmap`", source)
	out, err := os.ReadFile(filepath.Join(root, page))
	require.NoError(t, err, "reading %s: %v; run `make roadmap` to generate it", page, err)
	require.Equal(t, render(doc, source), string(out),
		"%s differs from what tools/roadmap generates from %s; run `make roadmap` to regenerate it", page, source)
}

// TestRoadmapIsGeneratedFromTheSexp holds ROADMAP.md to docs/roadmap.sexp.
func TestRoadmapIsGeneratedFromTheSexp(t *testing.T) {
	t.Parallel()
	checkGenerated(t, "docs/roadmap.sexp", "ROADMAP.md", roadmapdoc.Decode, roadmapdoc.Render)
}

// TestFixesIsGeneratedFromTheSexp holds FIXES.md to docs/fixes.sexp the same way.
func TestFixesIsGeneratedFromTheSexp(t *testing.T) {
	t.Parallel()
	checkGenerated(t, "docs/fixes.sexp", "FIXES.md", roadmapdoc.DecodeFixes, roadmapdoc.RenderFixes)
}
