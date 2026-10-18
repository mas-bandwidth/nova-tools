package roadmap

import (
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden.md from what the code renders")

// The diagrams page of testdata/diagrams.sexp is testdata/diagrams.golden.md byte for
// byte: every diagram the data supports, the mindmap cap, and the hostile labels
// swapped. Run go test ./pkg/roadmap -run TestDiagramsGolden -update to rewrite it.
func TestDiagramsGolden(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/diagrams.sexp")
	require.NoError(t, err)
	doc, err := Decode("testdata/diagrams.sexp", raw)
	require.NoError(t, err)
	page := RenderWithDiagrams(doc, "docs/roadmap.sexp")
	require.Equal(t, page, RenderWithDiagrams(doc, "docs/roadmap.sexp"), "the same bytes every time")
	if *update {
		require.NoError(t, os.WriteFile("testdata/diagrams.golden.md", []byte(page), 0o644))
	}
	want, err := os.ReadFile("testdata/diagrams.golden.md")
	require.NoError(t, err)
	require.Equal(t, string(want), page)
}

// Opting in adds only the At a glance section: removing it gives Render's bytes, so
// a repository that has not opted in sees no change.
func TestDiagramsAddOnlyTheirSection(t *testing.T) {
	t.Parallel()
	doc, err := Decode("good.sexp", []byte(good))
	require.NoError(t, err)
	section := Diagrams(doc)
	with := RenderWithDiagrams(doc, "x.sexp")
	require.Equal(t, Render(doc, "x.sexp"), strings.Replace(with, section, "", 1))
	require.Less(t, strings.Index(with, "## At a glance"), strings.Index(with, "## Scheduled in releases"), "the section sits near the top")
}

// The data decides which diagrams stand: one group draws no group pie, no done or
// scheduled record draws no done pie, one date draws no timeline; the mindmap always stands.
func TestDiagramsOnlyWhatTheDataSupports(t *testing.T) {
	t.Parallel()
	doc := &Doc{Title: "T", Text: "x", Groups: []Group{{ID: "g", Title: "G", Text: "x"}},
		Items: []Item{{ID: "a", Group: "g", Title: "A", Text: "x", Date: "2026-10-14"}, {ID: "b", Group: "g", Title: "B", Text: "x", Date: "2026-10-14"}}}
	d := Diagrams(doc)
	require.Equal(t, 1, strings.Count(d, "```mermaid"))
	require.Contains(t, d, "mindmap\n  root((\"T\"))\n    g1[\"G (2)\"]\n      g1i1[\"A\"]\n      g1i2[\"B\"]\n")
	doc.Items[1].Date = "2026-10-15"
	doc.Done = []Note{{ID: "d", Title: "D", Text: "x", Date: "2026-10-01"}}
	doc.Groups = append(doc.Groups, Group{ID: "h", Title: "H", Text: "x"})
	doc.Items = append(doc.Items, Item{ID: "c", Group: "h", Title: "C", Text: "x", Date: "2026-10-15"})
	d = Diagrams(doc)
	require.Equal(t, 4, strings.Count(d, "```mermaid"))
	require.Contains(t, d, "    \"Done\" : 1\n    \"On the roadmap\" : 3\n", "an empty slice is left out")
	require.Contains(t, d, "    2026-10-14 : 1 item\n    2026-10-15 : 2 items\n", "dates in order")
}

// Label leaves nothing that ends or reinterprets a quoted Mermaid label.
func TestLabelEscapesMermaid(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"plain words":            "plain words",
		`a "quote"`:              "a 'quote'",
		"a `tick`":               "a 'tick'",
		"issue #22":              "issue ＃22",
		"#quot; entity":          "＃quot, entity",
		"<b>html</b>":            "‹b›html‹/b›",
		"[a] {b}":                "(a) (b)",
		`back\slash`:             "back/slash",
		"two\n  lines\tjoined":   "two lines joined",
		"colon: and (parens) ok": "colon: and (parens) ok",
	}
	for in, want := range cases {
		require.Equal(t, want, Label(in), in)
		for _, bad := range []string{`"`, "`", "#", "<", ">", "[", "]", "{", "}", `\`, ";", "\n"} {
			require.NotContains(t, Label(in), bad, in)
		}
	}
}
