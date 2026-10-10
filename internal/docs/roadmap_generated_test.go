package docs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/roadmap"
)

// TestRoadmapIsGeneratedFromTheSexp holds ROADMAP.md to docs/roadmap.sexp: it
// decodes the committed sexp, renders it in process exactly as tools/roadmap
// does, and requires the committed page to be those bytes. A hand edit of the
// page, or a sexp edit without regenerating, is red, and the failure names the
// command that regenerates it.
func TestRoadmapIsGeneratedFromTheSexp(t *testing.T) {
	t.Parallel()

	root := testRoot(t)
	const source = "docs/roadmap.sexp"
	raw, err := os.ReadFile(filepath.Join(root, source))
	require.NoError(t, err, "reading %s: %v", source, err)
	doc, err := roadmap.Decode(source, raw)
	require.NoError(t, err, "%s does not decode; fix it, then run `make roadmap`", source)

	page, err := os.ReadFile(filepath.Join(root, "ROADMAP.md"))
	require.NoError(t, err, "reading ROADMAP.md: %v; run `make roadmap` to generate it", err)
	require.Equal(t, roadmap.Render(doc, source), string(page),
		"ROADMAP.md differs from what tools/roadmap generates from docs/roadmap.sexp; run `make roadmap` to regenerate it")
}

// TestFixesIsGeneratedFromTheSexp holds FIXES.md to docs/fixes.sexp the same way.
func TestFixesIsGeneratedFromTheSexp(t *testing.T) {
	t.Parallel()

	root := testRoot(t)
	const source = "docs/fixes.sexp"
	raw, err := os.ReadFile(filepath.Join(root, source))
	require.NoError(t, err, "reading %s: %v", source, err)
	fx, err := roadmap.DecodeFixes(source, raw)
	require.NoError(t, err, "%s does not decode; fix it, then run `make roadmap`", source)

	page, err := os.ReadFile(filepath.Join(root, "FIXES.md"))
	require.NoError(t, err, "reading FIXES.md: %v; run `make roadmap` to generate it", err)
	require.Equal(t, roadmap.RenderFixes(fx, source), string(page),
		"FIXES.md differs from what tools/roadmap generates from docs/fixes.sexp; run `make roadmap` to regenerate it")
}
