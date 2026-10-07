package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadTakenBackMayBeAskedAgainOfTheSameReader pins
// read-asked-again-after-takebackc-t-b. A read taken back with no verdict (a
// server restart's lapse, the reader away, a read deadline) is not an ask:
// that reader is asked the same attempt again, after every reader never asked,
// on the next identity <primary>.r<attempt>.<reader>.gN. A verdict is never
// asked again. The third take-back does not produce a fourth ask: the card
// raises the reads-exhausted judgment. No removed record is restored.
func TestReadTakenBackMayBeAskedAgainOfTheSameReader(t *testing.T) {
	t.Parallel()

	t.Run("never asked before a reader taken back", func(t *testing.T) {
		t.Parallel()
		w := reviewFlash(t)
		retireTakeback(w, "s1-1", "reader-a", 1)
		w.s.ReaderStates = map[string]string{"reader-a": ReaderUp, "reader-b": ReaderUp, "reader-c": ReaderUp}
		plan, _ := TickAsk(w.s, TickReq{})
		w.must(plan)
		got := readsAt(w.s, w.s.Work.Card("s1-1"), 1)
		require.Len(t, got, 1)
		assert.NotEqual(t, "reader-a", got[0].F("reader"), "a reader never asked is asked before one taken back")
		assert.NotContains(t, got[0].ID, ".g", "the first ask of a reader keeps the base id")
		old := w.s.Readers.Card(ReadCardID("s1-1", 1, "reader-a"))
		require.NotNil(t, old)
		assert.False(t, old.Placed(), "the taken-back record is not restored")
		assert.Nil(t, w.s.Readers.Card(ReadCardID("s1-1", 1, "reader-a")+".g1"))
	})

	t.Run("taken back is asked again once no one is left unasked", func(t *testing.T) {
		t.Parallel()
		w := reviewFlash(t)
		retireTakeback(w, "s1-1", "reader-a", 1)
		w.s.ReaderStates = map[string]string{"reader-a": ReaderUp, "reader-b": ReaderAway, "reader-c": ReaderDown}
		plan, _ := TickAsk(w.s, TickReq{})
		w.must(plan)
		id := ReadCardID("s1-1", 1, "reader-a") + ".g1"
		got := w.s.Readers.Placed(id)
		require.NotNil(t, got, "the same reader is asked again after a take-back with no verdict")
		assert.Equal(t, Asked, got.Col)
		assert.Equal(t, "reader-a", got.F("reader"))
		old := w.s.Readers.Card(ReadCardID("s1-1", 1, "reader-a"))
		require.NotNil(t, old)
		assert.False(t, old.Placed(), "the removed record stays removed")
		assert.Nil(t, w.s.Readers.Card(ReadCardID("s1-1", 1, "reader-b")+".g1"))
	})

	t.Run("a verdict is not asked again", func(t *testing.T) {
		t.Parallel()
		w := reviewPro(t)
		retireTakeback(w, "s1-1", "reader-a", 1)
		w.s.Readers.Put(&Card{ID: ReadCardID("s1-1", 1, "reader-b"), Row: "reader-b", Col: OK, Rev: 1,
			Fields: map[string]string{"kind": "read", "primary": "s1-1", "stream": "s1", "reader": "reader-b", "attempt": "1", "head": w.s.Work.Card("s1-1").F("head")}})
		w.s.ReaderStates = map[string]string{"reader-a": ReaderUp, "reader-b": ReaderUp, "reader-c": ReaderDown}
		plan, _ := TickAsk(w.s, TickReq{})
		w.must(plan)
		require.NotNil(t, w.s.Readers.Placed(ReadCardID("s1-1", 1, "reader-a")+".g1"), "the reader taken back is asked")
		assert.Nil(t, w.s.Readers.Card(ReadCardID("s1-1", 1, "reader-b")+".g1"), "a reader who gave a verdict is not asked again")
		still := w.s.Readers.Placed(ReadCardID("s1-1", 1, "reader-b"))
		require.NotNil(t, still)
		assert.Equal(t, OK, still.Col)
	})

	t.Run("the third take-back is not a fourth ask", func(t *testing.T) {
		t.Parallel()
		w := reviewFlash(t)
		retireTakeback(w, "s1-1", "reader-a", 1)
		retireTakeback(w, "s1-1", "reader-a", 2)
		retireTakeback(w, "s1-1", "reader-a", 3)
		w.s.ReaderStates = map[string]string{"reader-a": ReaderUp, "reader-b": ReaderDown, "reader-c": ReaderDown}
		plan, _ := TickAsk(w.s, TickReq{})
		w.must(plan)
		assert.Nil(t, w.s.Readers.Card(ReadCardID("s1-1", 1, "reader-a")+".g3"), "three take-backs do not produce a fourth ask")
		notes := w.notesOf(NReadsExhausted)
		require.Len(t, notes, 1, "the card raises reads exhausted: %+v refused %v", w.notes, plan.Refused)
		assert.Equal(t, []string{"s1-1"}, notes[0].Primaries)
		plan, _ = TickAsk(w.s, TickReq{})
		w.must(plan)
		assert.Len(t, w.notesOf(NReadsExhausted), 1, "the judgment stays open and is not written again")
		assert.Nil(t, w.s.Readers.Card(ReadCardID("s1-1", 1, "reader-a")+".g3"))
	})
}

// reviewFlash is one primary in review that needs one reader (flash).
func reviewFlash(t *testing.T) *world {
	t.Helper()
	w := setup(t, 1)
	toReview(w, "s1-1")
	c := w.s.Work.Card("s1-1")
	c.Fields["brief"] = ""
	delete(c.Fields, FieldTierNow)
	delete(c.Fields, FieldTier)
	require.Equal(t, 1, ReadsNeeded(c))
	return w
}

// reviewPro is one primary in review that needs two readers (pro).
func reviewPro(t *testing.T) *world {
	t.Helper()
	w := setup(t, 1)
	toReview(w, "s1-1")
	require.Equal(t, 2, ReadsNeeded(w.s.Work.Card("s1-1")))
	return w
}

// retireTakeback records a no-verdict take-back of reader at attempt 1: take 1
// is the base id, and each later take appends .gN. The record is not placed.
func retireTakeback(w *world, primary, reader string, take int) {
	w.t.Helper()
	id := ReadCardID(primary, 1, reader)
	if take >= 2 {
		id += ".g" + itoa(take-1)
	}
	w.s.Readers.Put(&Card{ID: id, Rev: 1, Fields: map[string]string{
		"kind": "read", "primary": primary, "stream": "s1", "reader": reader,
		"attempt": "1", "head": w.s.Work.Card(primary).F("head"),
		"asked": stamp(t0), "retired": stamp(t0), "retired_by": RetiredByLapsed,
	}})
}

// TestNoVerdictTakebackIsEveryTakebackWithoutAVerdict pins the reasons a read
// counts as taken back with no verdict toward the bound: the reader away, a
// restart's lapse, and a read deadline. A refusal is no verdict but not a
// take-back (ReadCardForAsk gives it one more ask, bound to nothing), and a
// level or a return is not one.
func TestNoVerdictTakebackIsEveryTakebackWithoutAVerdict(t *testing.T) {
	t.Parallel()
	for _, by := range []string{"away", "deadline", "restart", RetiredByLapsed} {
		c := &Card{Fields: map[string]string{"retired": stamp(t0), "retired_by": by}}
		assert.True(t, noVerdictTakeback(c), by)
	}
	for _, by := range []string{RetiredByRefused, RetiredByLevel, RetiredByCoordinator, "returned", "accept", ""} {
		c := &Card{Fields: map[string]string{"retired": stamp(t0), "retired_by": by}}
		assert.False(t, noVerdictTakeback(c), by)
	}
}
