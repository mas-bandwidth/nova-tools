package ntable_test

import (
	"math/rand"
	"strings"
	"testing"
	"testing/quick"
	"unicode"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/require"
)

func safeRenderedRows(text string, lines int) bool {
	if !utf8.ValidString(text) || strings.Count(text, "\n") != lines {
		return false
	}
	for _, r := range text {
		if r != '\n' && (unicode.IsControl(r) || r == '\u2028' || r == '\u2029' || (r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069')) {
			return false
		}
	}
	return true
}

func TestRenderEscapesEveryDisplayValueWithoutChangingTheSnapshot(t *testing.T) {
	t.Parallel()
	property := func(raw string) bool {
		tab := ntable.Table{Name: "table", FooterLabel: raw, Columns: []ntable.Column{
			{Name: "note", Label: raw, Projection: ntable.Text, Fold: ntable.None},
			{Name: "members", Projection: ntable.Members, Fold: ntable.Union},
			{Name: "first", Projection: ntable.First, Fold: ntable.None},
			{Name: "last", Projection: ntable.Last, Fold: ntable.None},
		}}
		row := ntable.NewRow(tab, "row")
		row.Label = raw
		row.Texts = map[string]string{"note": raw}
		for j := 1; j < len(row.Cells); j++ {
			row.Cells[j].Members = []ntable.Member{{Member: raw, Score: 1}}
		}
		tab.Rows = []ntable.Row{row}
		text := ntable.Render(tab, ntable.RenderOpts{Title: raw})
		return safeRenderedRows(text, 5) && tab.FooterLabel == raw && tab.Columns[0].Label == raw && tab.Rows[0].Label == raw && tab.Rows[0].Texts["note"] == raw && tab.Rows[0].Cells[1].Members[0].Member == raw
	}
	for _, raw := range []string{"first\nsecond", "\x1b[2Jclear", "\x00\r\t\x7f", "bad\xffutf8", "a\u2028b\u2029c\u202ed\u2066e", "ordinary \\ text", ""} {
		require.True(t, property(raw), "unsafe or mutated display for %q", raw)
	}
	require.NoError(t, quick.Check(property, &quick.Config{MaxCount: 500, Rand: rand.New(rand.NewSource(4457))}))
}
