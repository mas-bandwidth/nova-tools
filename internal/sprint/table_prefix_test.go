package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func tableIDs(cs []*Card) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

// Cards and WithPrefix walk the table's ids in id order, and stay true as cards
// are put and dropped after the order was first built: a step reads a primary's
// read cards by their id prefix, never by sorting the whole table each time.
func TestTableWithPrefixFollowsPutAndDrop(t *testing.T) {
	t.Parallel()
	tb := NewTable(Fleet)
	for _, id := range []string{"a-2.r1.x", "a-1.r1.y", "a-1.w1", "a-1.r1.x", "a-10.r1.x"} {
		tb.Put(&Card{ID: id})
	}
	require.Equal(t, []string{"a-1.r1.x", "a-1.r1.y", "a-1.w1", "a-10.r1.x", "a-2.r1.x"}, tableIDs(tb.Cards()))
	require.Equal(t, []string{"a-1.r1.x", "a-1.r1.y"}, tableIDs(tb.WithPrefix("a-1.r1.")))

	// a new id is placed in order, a replaced card is the new one, a dropped id is gone
	tb.Put(&Card{ID: "a-1.r1.w"})
	tb.Put(&Card{ID: "a-1.r1.y", Col: "moved"})
	tb.Drop("a-1.r1.x")
	got := tb.WithPrefix("a-1.r1.")
	require.Equal(t, []string{"a-1.r1.w", "a-1.r1.y"}, tableIDs(got))
	require.Equal(t, "moved", got[1].Col)
	require.Equal(t, []string{"a-1.r1.w", "a-1.r1.y", "a-1.w1", "a-10.r1.x", "a-2.r1.x"}, tableIDs(tb.Cards()))
	require.Empty(t, tb.WithPrefix("b-"))

	// a frozen copy keeps its own order as the table moves on
	fz := tb.Frozen()
	tb.Put(&Card{ID: "a-1.r1.z"})
	require.Equal(t, []string{"a-1.r1.w", "a-1.r1.y"}, tableIDs(fz.WithPrefix("a-1.r1.")))
	require.Equal(t, []string{"a-1.r1.w", "a-1.r1.y", "a-1.r1.z"}, tableIDs(tb.WithPrefix("a-1.r1.")))

	var none *Table
	require.Nil(t, none.WithPrefix("a"))
}

// AnyWithPrefix says what WithPrefix would find, without gathering it.
func TestTableAnyWithPrefixIsWithPrefixNotEmpty(t *testing.T) {
	t.Parallel()
	tb := NewTable(Readers)
	for _, id := range []string{"a-1.r1.reader-a", "a-1.r2.reader-b.g1", "a-10.r1.reader-a"} {
		tb.Put(&Card{ID: id})
	}
	for _, prefix := range []string{"a-1.r1.", "a-1.r2.", "a-1.r3.", "a-10.r1.", "a-2.r1.", "", "z"} {
		require.Equal(t, len(tb.WithPrefix(prefix)) > 0, tb.AnyWithPrefix(prefix), prefix)
	}
	var none *Table
	require.False(t, none.AnyWithPrefix(""))
}
