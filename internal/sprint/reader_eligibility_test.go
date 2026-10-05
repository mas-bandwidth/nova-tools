package sprint

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A reader who gave a verdict or a return is asked an attempt once, and the
// next attempt is read on new cards: the readers of an earlier attempt are
// eligible again (docs/SPEC-SPRINT.md section 6; tla/DirtyTick.tla,
// NewAttempt: "no reader has read it"). A take-back with no verdict is not
// that ask (read-asked-again-after-takebackc). The night of 2026-10-03: five
// cards at one attempt, each read returned by five readers, and five
// judgments "cannot ask"; the five are one judgment, "no eligible reader
// for <ids>" (TickAsk, cannotAskCond).

// TestTheReadersOfAnEarlierAttemptAreAskedTheNext pins the rule as it stands:
// the two readers that read attempt 1 are the only readers up when attempt 2
// comes back, and attempt 2 is asked of them.
func TestTheReadersOfAnEarlierAttemptAreAskedTheNext(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	toReview(w, "s1-1")
	w.must(Ask(w.s, AskReq{}))
	first := readsAt(w.s, w.s.Work.Card("s1-1"), 1)
	require.Len(t, first, 2)
	w.must(Read(w.s, ReadReq{As: first[0].F("reader"), Verdict: "ok", Sel: Sel{IDs: []string{first[0].ID}}}))
	w.must(Read(w.s, ReadReq{As: first[1].F("reader"), Verdict: "broken", Finding: "line 1: off by one", Sel: Sel{IDs: []string{first[1].ID}}}))
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "off by one"}))
	card := w.s.Work.Card("s1-1").F("work")
	require.Equal(t, "s1-1.w2", card)
	member := w.s.Fleet.Card(card).Row
	w.must(Take(w.s, TakeReq{As: member, Sel: Sel{IDs: []string{card}}, Gens: gensOf(w.s, card)}))
	w.must(Finish(w.s, FinishReq{As: member, Sel: Sel{IDs: []string{card}}, Gens: gensOf(w.s, card)}))
	// only the two readers of attempt 1 are up
	w.s.ReaderStates = map[string]string{"reader-a": ReaderAway, "reader-b": ReaderAway, "reader-c": ReaderAway}
	for _, rc := range first {
		w.s.ReaderStates[rc.F("reader")] = ReaderUp
	}
	plan, _ := TickAsk(w.s, TickReq{})
	w.must(plan)
	assert.Empty(t, w.notesOf(NCannotAsk), "the readers of attempt 1 are eligible at attempt 2")
	assert.ElementsMatch(t, readerNames(first), readerNames(readsAt(w.s, w.s.Work.Card("s1-1"), 2)))
}

// burnedWorld is n flash primaries in review at attempt 1 and five readers
// up, each holding a read card at that attempt retired by a return: a return
// is a verdict for this rule, so no reader may be asked any of them.
func burnedWorld(t *testing.T, n int) *world {
	t.Helper()
	readers := []string{"reader-a", "reader-b", "reader-c", "reader-d", "reader-e"}
	w := newWorld(t, readers...)
	w.s.Work.SetRows(append(w.s.Work.Rows(), "s1"))
	w.s.ReaderStates = map[string]string{}
	for _, rd := range readers {
		w.s.ReaderStates[rd] = ReaderUp
	}
	for i := 1; i <= n; i++ {
		burn(w, fmt.Sprintf("p%d", i), readers)
	}
	return w
}

// burn adds the flash primary p in review at attempt 1 with a read card of
// every reader at that attempt retired by a return.
func burn(w *world, p string, readers []string) {
	w.s.Work.Put(&Card{ID: p, Row: "s1", Col: Review, Score: float64(len(w.s.Work.Cards())), Rev: 1,
		Fields: map[string]string{"kind": "primary", "attempt": "1", "stream": "s1", "head": "h1"}})
	for _, rd := range readers {
		w.s.Readers.Put(&Card{ID: ReadCardID(p, 1, rd), Rev: 1, Fields: map[string]string{"kind": "read", "primary": p, "stream": "s1",
			"reader": rd, "attempt": "1", "head": "h1", "asked": stamp(t0), "retired": stamp(t0), "retired_by": "returned"}})
	}
}

func TestThePrimariesNoReaderMayBeAskedAreOneJudgment(t *testing.T) {
	t.Parallel()
	w := burnedWorld(t, 5)
	ids := []string{"p1", "p2", "p3", "p4", "p5"}
	plan, _ := TickAsk(w.s, TickReq{})
	w.must(plan)
	notes := w.notesOf(NCannotAsk)
	require.Len(t, notes, 1, "five primaries no reader may be asked are one judgment: %v", notes)
	assert.ElementsMatch(t, ids, notes[0].Primaries, "every primary is a subject of it")
	assert.Equal(t, 5, notes[0].Count)
	assert.True(t, strings.HasPrefix(notes[0].What, NoEligibleReader+"p1, p2, p3, p4, p5: "), "it names them: %q", notes[0].What)
	assert.Contains(t, notes[0].What, "a reader is asked an attempt once", "and says the rule: %q", notes[0].What)
	assert.Contains(t, notes[0].What, "rework p1 --fix", "and the way out: %q", notes[0].What)
	for _, id := range ids {
		assert.Len(t, w.openOn(id), 1, "%s is held by it", id)
	}
	// the next tick writes nothing: the judgment stands
	plan, _ = TickAsk(w.s, TickReq{})
	w.must(plan)
	assert.Len(t, w.notesOf(NCannotAsk), 1, "a judgment once")
	// a sixth joins: one more judgment, naming it alone
	burn(w, "p6", w.s.Readers.Rows())
	plan, _ = TickAsk(w.s, TickReq{})
	w.must(plan)
	notes = w.notesOf(NCannotAsk)
	require.Len(t, notes, 2)
	assert.Equal(t, []string{"p6"}, notes[1].Primaries, "the primaries judged already are not its subjects")
	assert.True(t, strings.HasPrefix(notes[1].What, NoEligibleReader+"p6: "), "%q", notes[1].What)
	for _, id := range append(ids, "p6") {
		assert.Len(t, w.openOn(id), 1, "%s is held once", id)
	}
	// a reader added, with room for all, asks every one and closes its
	// subject; an asked primary is not judged stranded
	w.s.Readers.SetRows(append(w.s.Readers.Rows(), "reader-f"))
	w.s.ReaderStates["reader-f"] = ReaderUp
	plan, _ = TickAsk(w.s, TickReq{})
	w.must(plan)
	assert.Len(t, w.notesOf(NCannotAsk), 2, "no new judgment")
	assert.Empty(t, w.notesOf(NStranded), "a primary asked this tick is not stranded")
	for _, id := range append(ids, "p6") {
		asked := len(readsAt(w.s, w.s.Work.Card(id), 1))
		assert.Equal(t, asked == 0, len(w.openOn(id)) == 1, "%s: asked %d, open %v", id, asked, w.openOn(id))
	}
}
