package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// readCardsWorld is a world with read cards on (set --read-cards on), the members up at
// the width with a reader row each (reader-<m>: the reader role), and nothing else.
func readCardsWorld(t *testing.T, width int, members ...string) *world {
	t.Helper()
	var readers []string
	for _, m := range members {
		readers = append(readers, ReaderPrefix+m)
	}
	w := newWorld(t, readers...)
	for _, m := range members {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m, Width: width}))
	}
	w.s.Work.SetProp(PropReadCards, ReadCardsOnWord)
	return w
}

// putReviewBy puts a primary in review at attempt 1 whose work card was worked by unit.
func putReviewBy(w *world, id, brief, unit string, score float64) {
	putReview(w, id, brief, 1, score, "head-"+id)
	putAttemptWork(w, id, 1, "sprint/"+id, "work-"+id, "yes")
	w.s.Fleet.Card(WorkCardID(id, 1)).Fields["member"] = unit
}

// readCardsOf is the placed read cards of the primary on the fleet table.
func readCardsOf(w *world, primary string) []*Card {
	var out []*Card
	for _, c := range w.s.Fleet.Of(primary) {
		if isRead(c) {
			out = append(out, c)
		}
	}
	return out
}

// TestAReviewOpensNReadCardsAtOnce pins layer 1 of read cards (docs/SPEC-SPRINT.md
// section 6, "A read is a consumer card"): a pro primary in review is asked both its reads
// in the one ask, as two read cards on the fleet table, each dealt to a different reader
// in the step that cuts it, named <primary>.r<attempt>.<reader>, kind read, at the
// primary's tier, its level inherited, in ready on the reader's row; the readers table is
// asked nothing. A flash card is asked its one read.
func TestAReviewOpensNReadCardsAtOnce(t *testing.T) {
	t.Parallel()
	w := readCardsWorld(t, 4, "m1", "m2", "m3")
	putReviewBy(w, "s1-1", "s1-1: work (s1) tier: pro\nPRIORITY: high\n", "m1", 1)
	w.s.Work.Card("s1-1").Fields[FieldPriority] = PriorityHigh
	putReviewBy(w, "s1-2", "s1-2: work (s1)\n", "m1", 2)
	askReaders(t, w, nil)

	pro := readCardsOf(w, "s1-1")
	require.Len(t, pro, 2, "a pro card's two reads are cut at once")
	seen := map[string]bool{}
	for _, c := range pro {
		require.Equal(t, "read", c.F("kind"))
		require.Equal(t, ReadCardID("s1-1", 1, c.F("reader")), c.ID)
		require.Equal(t, c.F("reader"), c.Row, "dealt to the reader it names")
		require.Equal(t, Ready, c.Col)
		require.Equal(t, "pro", c.F(FieldTier), "the read starts at the card's tier")
		require.Equal(t, PriorityHigh, c.F(FieldPriority), "the read inherits its primary's level")
		require.Equal(t, "work-s1-1", c.F("head"))
		require.NotEqual(t, "m1", c.Row, "never the worker's")
		require.NotEmpty(t, c.F(FieldReadDeadline))
		seen[c.Row] = true
	}
	require.Len(t, seen, 2, "two different readers")
	flash := readCardsOf(w, "s1-2")
	require.Len(t, flash, 1, "a flash card's one read")
	require.Equal(t, "flash", flash[0].F(FieldTier))
	for _, c := range w.s.Readers.Cards() {
		require.False(t, c.Placed(), "the readers table is asked nothing while read cards are on")
	}
	require.Equal(t, Review, w.state("s1-1"), "the primary stays in review while it is read")

	// asked again, nothing more is cut: the reads stand
	askReaders(t, w, nil)
	require.Len(t, readCardsOf(w, "s1-1"), 2)
	require.Len(t, readCardsOf(w, "s1-2"), 1)
}

// TestReadCardsOffAskAsBefore pins the setting: with read cards off the readers table is
// asked, one read at a time, as before.
func TestReadCardsOffAskAsBefore(t *testing.T) {
	t.Parallel()
	w := readCardsWorld(t, 4, "m1", "m2", "m3")
	w.s.Work.SetProp(PropReadCards, "off")
	putReviewBy(w, "s1-1", "s1-1: work (s1) tier: pro\n", "m1", 1)
	askReaders(t, w, nil)
	require.Empty(t, readCardsOf(w, "s1-1"))
	require.NotNil(t, placedReaderRead(w, "s1-1"))
}
