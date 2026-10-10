package roadmap

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const good = `; a comment is text
(roadmap "v1"
 :title "A roadmap"
 :text "The opening
        paragraph."
 :scheduled ((scheduled "s1" :release "v1.3" :title "Moved" :text "Into v1.3." :date "2026-10-09"))
 :done ((done "d1" :title "Settled" :text "In place." :date "2026-10-09"))
 :groups ((group "g1" :title "First group" :text "What it holds."))
 :items ((item "i1" :group "g1" :title "One item" :text "What it is." :why "Not a fix." :date "2026-10-09"
          :exists "a/b.go:1 has part" :cards 2)
         (item "i2" :group "g1" :title "Two" :text "More." :why "Later." :date "2026-10-09")
         (item "i3" :group "g1" :title "Three" :text "From elsewhere." :date "2026-10-10"
          :origin "issue #7" :release "v1.3")))`

// A good file decodes into its records, and renders the same bytes every time,
// with text joined however the file wraps it.
func TestRoadmapDecodesAndRendersTheShape(t *testing.T) {
	t.Parallel()
	doc, err := Decode("good.sexp", []byte(good))
	require.NoError(t, err)
	require.Len(t, doc.Items, 3)
	require.Equal(t, 2, doc.Items[0].Cards)
	require.Equal(t, map[string]int{"g1": 3}, doc.GroupCount())
	require.Equal(t, Item{ID: "i3", Group: "g1", Title: "Three", Text: "From elsewhere.", Date: "2026-10-10",
		Origin: "issue #7", Release: "v1.3"}, doc.Items[2], ":why is optional; :origin and :release decode")
	page := Render(doc, "x.sexp")
	require.Equal(t, page, Render(doc, "x.sexp"))
	for _, want := range []string{
		"# A roadmap\n",
		"The opening paragraph.\n",
		"- **Moved** (v1.3). Into v1.3.\n",
		"- **Settled** (2026-10-09). In place.\n",
		"- [First group](#first-group) (3)\n",
		"### One item\n\nWhat it is.\n\nAlready in the code: a/b.go:1 has part\n\nWhy it waits: Not a fix.\n",
		"Replaces 2 open sprint cards, each mapped to `i1`.\n",
		"`make roadmap`",
		"### Three\n\nFrom elsewhere.\n\nTarget: v1.3\n\nFrom: issue #7\n",
	} {
		require.Contains(t, page, want)
	}
	require.NotContains(t, page[strings.Index(page, "### Three"):], "Why it waits", "an item with no :why renders none")
}

// Every shape problem is refused, named, and the whole file with it.
func TestRoadmapRefusesEveryShapeProblem(t *testing.T) {
	t.Parallel()
	cases := map[string]struct{ from, to, want string }{
		"unknown key":     {`:why "Later."`, `:why "Later." :colour "red"`, "unknown key :colour"},
		"repeated key":    {`:why "Later."`, `:why "Later." :why "Again."`, "key :why stands twice"},
		"missing key":     {`:title "Two" `, ``, "missing key :title"},
		"empty origin":    {`:origin "issue #7"`, `:origin ""`, ":origin wants a non-empty string"},
		"unknown group":   {`(item "i2" :group "g1"`, `(item "i2" :group "g9"`, `:group "g9" names no (group ...) record`},
		"duplicate id":    {`(item "i2"`, `(item "i1"`, `id "i1" is already used by a (item ...) record`},
		"bad date":        {`:why "Later." :date "2026-10-09"`, `:why "Later." :date "Oct 9"`, `wants YYYY-MM-DD`},
		"wrong kind":      {`:cards 2`, `:cards "c1"`, ":cards wants a positive integer"},
		"zero cards":      {`:cards 2`, `:cards 0`, ":cards wants a positive integer"},
		"empty string":    {`:title "Two"`, `:title "  "`, ":title wants a non-empty string"},
		"wrong format":    {`(roadmap "v1"`, `(roadmap "v2"`, `want one (roadmap "v1" ...) form`},
		"evaluation form": {`:title "Two"`, `:title #.(x)`, "dispatch macro"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src := strings.Replace(good, c.from, c.to, 1)
			require.NotEqual(t, good, src, "the case must change the file")
			doc, err := Decode("bad.sexp", []byte(src))
			require.Nil(t, doc)
			require.ErrorContains(t, err, c.want)
			require.ErrorContains(t, err, "bad.sexp")
		})
	}
}

// A group that holds no item is refused: an empty heading is a record nobody finished.
func TestRoadmapRefusesAnEmptyGroup(t *testing.T) {
	t.Parallel()
	src := strings.Replace(good, `:groups ((group "g1" :title "First group" :text "What it holds."))`,
		`:groups ((group "g1" :title "First group" :text "What it holds.") (group "g2" :title "Empty" :text "None."))`, 1)
	_, err := Decode("bad.sexp", []byte(src))
	require.ErrorContains(t, err, `group "g2" holds no item`)
}
