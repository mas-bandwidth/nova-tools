package sprint

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// cannotAskOpen is the open cannot-ask judgments on the primary.
func cannotAskOpen(w *world, id string) []Open {
	return closesFor(w.s.Open, []string{NCannotAsk}, id)
}

// A primary in review that no reader of its tier may ever read (here: its only reader row
// besides its worker spent its read) is the cannot-ask judgment: raised by the first tick's
// ask and not again by the next while it holds, and the waiting mark the primary carried
// from before is cleared with it, so the card never shows both.
func TestCannotAskIsRaisedOnceAndClearsTheWaitingMark(t *testing.T) {
	t.Parallel()
	w := readCardsWorld(t, 4, "m1", "m2")
	putReviewBy(w, "s1-1", "s1-1: work (s1) tier: pro\n", "m1", 1)
	dealReads(t, w, nil)
	require.Len(t, readCardsOf(w, "s1-1"), 1, "m2's read cut; the pro card's second has no reader that may take it")
	w.s.Work.Card("s1-1").Fields[FieldWaitingReader] = "1 " + stamp(t0)

	first := w.part(readCardsAskPart, TickReq{})
	require.Len(t, cannotAskOpen(w, "s1-1"), 1, "the first tick raises cannot ask: %v", first.Units)
	require.Empty(t, w.s.Work.Card("s1-1").F(FieldWaitingReader), "the waiting mark is cleared with it")

	second := w.part(readCardsAskPart, TickReq{})
	require.Len(t, cannotAskOpen(w, "s1-1"), 1, "still the one judgment")
	require.Empty(t, second.Notes, "the second tick writes no judgment again")
	require.Empty(t, w.s.Work.Card("s1-1").F(FieldWaitingReader), "and no waiting mark")
}

// A member reader the coordinator retired (reader retire: ReaderRetired) is dealt no read
// card (memberReads), so it is no reader that may yet read: a primary whose only reader of
// its tier besides its worker is retired is the cannot-ask judgment, never a silent wait.
func TestARetiredMemberReaderLeavesTheReadToTheCannotAskJudgment(t *testing.T) {
	t.Parallel()
	w := readCardsWorld(t, 4, "m1", "m2")
	w.s.ReaderStates = map[string]string{ReaderPrefix + "m2": ReaderRetired}
	putReviewBy(w, "s1-1", "s1-1: work (s1)\n", "m1", 1)
	dealReads(t, w, nil)
	require.Empty(t, readCardsOf(w, "s1-1"), "a retired reader is dealt nothing")
	require.False(t, readWaitHeld(w.s, w.s.Work.Card("s1-1")), "no reader that may yet read it: no held wait")
	w.part(readCardsAskPart, TickReq{})
	open := cannotAskOpen(w, "s1-1")
	require.Len(t, open, 1, "the cannot-ask judgment")
	require.Contains(t, open[0].Note.What, NoEligibleReader+"s1-1")
}
