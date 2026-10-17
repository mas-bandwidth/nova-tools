package roadmap

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const tables = `(roadmap "v1"
 :title "Tables"
 :text "The page."
 :scheduled ((scheduled "s1" :release "v1.3" :title "Scheduled one" :text "S." :date "2026-10-09"))
 :done ((done "d1" :title "Done one" :text "D." :date "2026-10-10"))
 :groups ((group "g1" :title "First" :text "One.") (group "g2" :title "Second" :text "Two."))
 :items ((item "i1" :group "g1" :title "With | pipe" :text "T." :date "2026-10-09" :release "v1.5" :cards 2 :exists "a.go:1")
         (item "i2" :group "g1" :title "Plain" :text "P." :date "2026-10-09")
         (item "i3" :group "g2" :title "Another" :text "A." :date "2026-10-09")))`

// The page carries a generated feature table under every heading that lists
// entries: the scheduled notes, the done notes, and each group's items, before
// that heading's existing prose, which is unchanged.
func TestRenderFeatureTables(t *testing.T) {
	t.Parallel()
	doc, err := Decode("tables.sexp", []byte(tables))
	require.NoError(t, err)
	page := Render(doc, "tables.sexp")
	for _, want := range []string{
		"## Scheduled in releases\n\n| feature | done | release |\n|---|:---:|---|\n| Scheduled one | ❌ | v1.3 |\n",
		"## Done\n\n| feature | done | date |\n|---|:---:|---|\n| Done one | ✅ | 2026-10-10 |\n",
		"## First\n\n| feature | done | release | cards | in code |\n|---|:---:|---|---:|---|\n| With \\| pipe | ❌ | v1.5 | 2 | partial |\n| Plain | ❌ | — | — | — |\n",
		"## Second\n\n| feature | done | release | cards | in code |\n|---|:---:|---|---:|---|\n| Another | ❌ | — | — | — |\n",
		"These were roadmap items and now have a release. They are listed here so nobody looks for them below.\n\n- **Scheduled one** (v1.3). S.\n",
		"Decided and in place. Recorded so the items that measure them have their starting point.\n\n- **Done one** (2026-10-10). D.\n",
	} {
		require.Contains(t, page, want)
	}
	scheduled := strings.Index(page, "## Scheduled in releases")
	require.Less(t, strings.Index(page, "| Scheduled one |"), strings.Index(page, "These were roadmap items"))
	require.Less(t, scheduled, strings.Index(page, "| Scheduled one |"))
	done := strings.Index(page, "## Done")
	require.Less(t, done, strings.Index(page, "| Done one |"))
	require.Less(t, strings.Index(page, "| Done one |"), strings.Index(page, "Decided and in place."))

	empty := &Doc{Title: "Empty", Text: "Nothing.", Groups: []Group{{ID: "g", Title: "Empty group", Text: "No items."}}}
	blank := Render(empty, "empty.sexp")
	require.NotContains(t, blank, "| feature |", "a section with no entries gets no table")
}

var tableRow = regexp.MustCompile(`(?m)^\| .+ \| (✅|❌) \| `)

// The number of table rows on the page is the number of entries in the data:
// scheduled notes, done notes, and items.
func TestRenderTableRowsEqualEntries(t *testing.T) {
	t.Parallel()
	doc, err := Decode("tables.sexp", []byte(tables))
	require.NoError(t, err)
	page := Render(doc, "tables.sexp")
	rows := len(tableRow.FindAllStringIndex(page, -1))
	require.Equal(t, len(doc.Scheduled)+len(doc.Done)+len(doc.Items), rows, "every entry has exactly one table row")
}
