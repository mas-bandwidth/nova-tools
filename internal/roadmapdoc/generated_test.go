package roadmapdoc

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRoadmapIsGeneratedFromTheSexp holds docs/ROADMAP.md to docs/roadmap.sexp: decode the sexp,
// render it in process exactly as tools/roadmap does, and require the page to be those bytes. A
// hand edit of the page, or a sexp edit without regenerating, is red, and the failure names the
// command that regenerates it. The package's own tests live in nova-sprint, where this code is from.
func TestRoadmapIsGeneratedFromTheSexp(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	const source, page = "docs/roadmap.sexp", "docs/ROADMAP.md"
	raw, err := os.ReadFile(filepath.Join(root, source))
	if err != nil {
		t.Fatalf("reading %s: %v", source, err)
	}
	doc, err := Decode(source, raw)
	if err != nil {
		t.Fatalf("%s does not decode; fix it, then run `make roadmap`: %v", source, err)
	}
	out, err := os.ReadFile(filepath.Join(root, page))
	if err != nil {
		t.Fatalf("reading %s: %v; run `make roadmap` to generate it", page, err)
	}
	if got := Render(doc, source); got != string(out) {
		t.Fatalf("%s differs from what tools/roadmap generates from %s; run `make roadmap` to regenerate it", page, source)
	}
}
