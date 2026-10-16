// roadmap writes docs/ROADMAP.md from docs/roadmap.sexp, so the page is never written by hand:
// the sexp is the data, internal/roadmapdoc decodes it through internal/worklang (nothing is
// evaluated) and renders the page, and internal/roadmapdoc's TestRoadmapIsGeneratedFromTheSexp
// holds the committed page to what this writes.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/roadmapdoc"
)

func main() {
	file := flag.String("file", "docs/roadmap.sexp", "the data to read")
	out := flag.String("out", "docs/ROADMAP.md", "where to write the rendered page")
	flag.Parse()
	if flag.NArg() > 0 {
		refuse("takes no positional words: " + strings.Join(flag.Args(), " "))
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		refuse(fmt.Sprintf("reading %s: %v", *file, err))
	}
	doc, err := roadmapdoc.Decode(*file, raw)
	if err != nil {
		refuse(oneLine(err.Error()))
	}
	page, items := roadmapdoc.Render(doc, *file), len(doc.Items)
	if err := os.WriteFile(*out, []byte(page), 0o644); err != nil {
		refuse(fmt.Sprintf("writing %s: %v", *out, err))
	}
	fmt.Printf("roadmap OK items=%d file=%s\n", items, *out)
}

// refuse prints one line naming what was wrong and what to do, and exits 2.
func refuse(why string) {
	fmt.Fprintf(os.Stderr, "roadmap REFUSED: %s; run: make roadmap\n", why)
	os.Exit(2)
}

// oneLine joins a multi-problem error onto one line.
func oneLine(s string) string { return strings.ReplaceAll(s, "\n", "; ") }
