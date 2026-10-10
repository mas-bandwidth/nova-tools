// roadmap writes ROADMAP.md from docs/roadmap.sexp (and, with --kind fixes,
// FIXES.md from docs/fixes.sexp), so neither page is ever written by hand: the
// sexp is the data, internal/roadmap decodes it through internal/roadmap/sexp
// (nothing is evaluated) and renders the page, and
// internal/docs's TestRoadmapIsGeneratedFromTheSexp and
// TestFixesIsGeneratedFromTheSexp hold the committed pages to what this writes.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/roadmap"
)

func main() {
	kind := flag.String("kind", "roadmap", "what the file holds: roadmap (docs/roadmap.sexp) or fixes (docs/fixes.sexp)")
	file := flag.String("file", "docs/roadmap.sexp", "the data to read")
	out := flag.String("out", "ROADMAP.md", "where to write the rendered page")
	flag.Parse()
	if flag.NArg() > 0 {
		refuse("takes no positional words: " + strings.Join(flag.Args(), " "))
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		refuse(fmt.Sprintf("reading %s: %v", *file, err))
	}
	var page string
	var items int
	switch *kind {
	case "roadmap":
		doc, err := roadmap.Decode(*file, raw)
		if err != nil {
			refuse(oneLine(err.Error()))
		}
		page, items = roadmap.Render(doc, *file), len(doc.Items)
	case "fixes":
		fx, err := roadmap.DecodeFixes(*file, raw)
		if err != nil {
			refuse(oneLine(err.Error()))
		}
		page, items = roadmap.RenderFixes(fx, *file), len(fx.Items)
	default:
		refuse(fmt.Sprintf("--kind %q is not roadmap or fixes", *kind))
	}
	if err := os.WriteFile(*out, []byte(page), 0o644); err != nil {
		refuse(fmt.Sprintf("writing %s: %v", *out, err))
	}
	fmt.Printf("%s OK items=%d file=%s\n", *kind, items, *out)
}

// refuse prints one line naming what was wrong and what to do, and exits 2.
func refuse(why string) {
	fmt.Fprintf(os.Stderr, "roadmap REFUSED: %s; run: make roadmap\n", why)
	os.Exit(2)
}

// oneLine joins a multi-problem error onto one line.
func oneLine(s string) string { return strings.ReplaceAll(s, "\n", "; ") }
