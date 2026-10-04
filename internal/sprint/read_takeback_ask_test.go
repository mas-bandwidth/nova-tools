package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A read taken back with no verdict (a server restart, a reader marked away,
// a read deadline) does not count as asked: its reader may be asked the same
// attempt again, on a new read card (ReadCardIDAt), after the readers never
// asked it. A reader that gave a verdict is never asked the attempt again, and
// a reader whose read of one attempt was taken back MaxReadAsks times is not
// asked it again: the card raises reads exhausted (tla/DirtyTick.tla,
// TakeBacksBounded; docs/SPEC-SPRINT.md, read-asked-again-after-takebackc.w1).

// takeBackWorld is a pro primary p1 in review at attempt 1, readers up, no read.
func takeBackWorld(t *testing.T, readers ...string) *world {
	t.Helper()
	w := newWorld(t, readers...)
	w.s.Work.SetRows(append(w.s.Work.Rows(), "s1"))
	w.s.ReaderStates = map[string]string{}
	for _, r := range readers {
		w.s.ReaderStates[r] = ReaderUp
	}
	w.s.Work.Put(&Card{ID: "p1", Row: "s1", Col: Review, Score: 1, Rev: 1,
		Fields: map[string]string{"kind": "primary", "attempt": "1", "stream": "s1", "head": "h1", "brief": proBrief}})
	return w
}

// takeBack takes the reader's placed read of p1 back with no verdict, as the
// ask and the sweep do (retired by away).
func takeBack(w *world, reader string) {
	w.t.Helper()
	rc := placedRead(w.s, "p1", 1, reader)
	require.NotNil(w.t, rc, "%s holds a read of p1 to take back", reader)
	w.entry(change(Readers, removeEntry(rc, map[string]string{"retired": stamp(w.s.Now), "retired_by": "away"})))
}

// retiredRead puts a retired read card of p1 on the reader at the id.
func retiredRead(w *world, id, reader string, fields map[string]string) {
	f := map[string]string{"kind": "read", "primary": "p1", "stream": "s1", "reader": reader, "attempt": "1", "head": "h1",
		"asked": stamp(t0), "retired": stamp(t0)}
	for k, v := range fields {
		f[k] = v
	}
	w.s.Readers.Put(&Card{ID: id, Rev: 1, Fields: f})
}

func placedReaders(w *world) []string { return readerNames(readsAt(w.s, w.s.Work.Card("p1"), 1)) }

func TestReadTakenBackMayBeAskedAgainOfTheSameReader(t *testing.T) {
	t.Parallel()

	t.Run("a card whose only two readers were taken back by a restart is asked of them again", func(t *testing.T) {
		t.Parallel()
		w := takeBackWorld(t, "r1", "r2")
		w.must(func() Plan { p, _ := TickAsk(w.s, TickReq{}); return p }())
		require.ElementsMatch(t, []string{"r1", "r2"}, placedReaders(w))
		takeBack(w, "r1")
		takeBack(w, "r2")
		require.Empty(t, placedReaders(w), "the restart took both reads back")
		plan, _ := TickAsk(w.s, TickReq{})
		w.must(plan)
		assert.ElementsMatch(t, []string{"r1", "r2"}, placedReaders(w), "each reader is asked again")
		for _, r := range []string{"r1", "r2"} {
			rc := placedRead(w.s, "p1", 1, r)
			assert.Equal(t, ReadCardIDAt("p1", 1, r, 2), rc.ID, "a new read card, the second ask")
			assert.Equal(t, Asked, rc.Col)
			old := w.s.Readers.Card(ReadCardID("p1", 1, r))
			require.NotNil(t, old)
			assert.False(t, old.Placed(), "the card taken back stays retired")
			assert.Equal(t, "away", old.F("retired_by"))
		}
		assert.Empty(t, w.notesOf(NCannotAsk), "no reader is lacking")
		assert.Empty(t, w.notesOf(NReadsExhausted))
		assert.Empty(t, w.notesOf(NStranded))
	})

	t.Run("a reader that gave a verdict is never asked again and the readers never asked come first", func(t *testing.T) {
		t.Parallel()
		w := takeBackWorld(t, "r1", "r2", "r3", "r4")
		retiredRead(w, ReadCardID("p1", 1, "r1"), "r1", map[string]string{"retired_by": "read", "verdict": "ok"})
		retiredRead(w, ReadCardID("p1", 1, "r2"), "r2", map[string]string{"retired_by": "away"})
		plan, _ := TickAsk(w.s, TickReq{})
		w.must(plan)
		assert.ElementsMatch(t, []string{"r3", "r4"}, placedReaders(w), "the readers never asked, not the one taken back")
		assert.Nil(t, w.s.Readers.Card(ReadCardIDAt("p1", 1, "r1", 2)), "the reader with a verdict has no second card")
		// r3 and r4 give way: only the reader taken back is left for the second read
		takeBack(w, "r3")
		takeBack(w, "r4")
		w.s.ReaderStates["r3"], w.s.ReaderStates["r4"] = ReaderAway, ReaderAway
		plan, _ = TickAsk(w.s, TickReq{})
		w.must(plan)
		assert.NotContains(t, placedReaders(w), "r1", "a reader with a verdict is never asked the attempt again")
		w.s.ReaderStates["r1"] = ReaderUp
		plan, _ = TickAsk(w.s, TickReq{})
		w.must(plan)
		assert.NotContains(t, placedReaders(w), "r1")
		assert.Nil(t, w.s.Readers.Card(ReadCardIDAt("p1", 1, "r1", 2)))
	})

	t.Run("a reader whose read was returned or levelled is not asked it again", func(t *testing.T) {
		t.Parallel()
		w := takeBackWorld(t, "r1", "r2")
		retiredRead(w, ReadCardID("p1", 1, "r1"), "r1", map[string]string{"retired_by": "returned"})
		retiredRead(w, ReadCardID("p1", 1, "r2"), "r2", map[string]string{"retired_by": RetiredByLevel})
		fresh, reask := w.s.askableReaders(w.s.Work.Card("p1"), 1)
		assert.Empty(t, fresh)
		assert.Empty(t, reask)
	})

	t.Run("a read taken back three times ends in reads exhausted", func(t *testing.T) {
		t.Parallel()
		w := takeBackWorld(t, "r1", "r2")
		for ask := 1; ask <= MaxReadAsks; ask++ {
			plan, _ := TickAsk(w.s, TickReq{})
			w.must(plan)
			require.ElementsMatch(t, []string{"r1", "r2"}, placedReaders(w), "ask %d", ask)
			assert.Equal(t, ReadCardIDAt("p1", 1, "r1", ask), placedRead(w.s, "p1", 1, "r1").ID)
			takeBack(w, "r1")
			takeBack(w, "r2")
		}
		assert.Empty(t, w.notesOf(NReadsExhausted), "not before the third take-back")
		plan, _ := TickAsk(w.s, TickReq{})
		w.must(plan)
		assert.Empty(t, placedReaders(w), "no fourth ask")
		assert.Nil(t, w.s.Readers.Card(ReadCardIDAt("p1", 1, "r1", MaxReadAsks+1)))
		notes := w.notesOf(NReadsExhausted)
		require.Len(t, notes, 1, "reads exhausted: %v", w.notesOf(NCannotAsk))
		assert.Equal(t, []string{"p1"}, notes[0].Primaries)
		assert.Empty(t, w.notesOf(NCannotAsk), "a judgment of its own, not no eligible reader")
		plan, _ = TickAsk(w.s, TickReq{})
		w.must(plan)
		assert.Len(t, w.notesOf(NReadsExhausted), 1, "a judgment once")
	})

	t.Run("a read taken back with a reader away is asked of it again when it is back", func(t *testing.T) {
		t.Parallel()
		w := takeBackWorld(t, "r1", "r2", "r3")
		retiredRead(w, ReadCardID("p1", 1, "r1"), "r1", map[string]string{"retired_by": "away"})
		retiredRead(w, ReadCardID("p1", 1, "r2"), "r2", map[string]string{"retired_by": "away"})
		retiredRead(w, ReadCardID("p1", 1, "r3"), "r3", map[string]string{"retired_by": "away"})
		w.s.ReaderStates["r2"], w.s.ReaderStates["r3"] = ReaderAway, ReaderAway
		plan, _ := TickAsk(w.s, TickReq{})
		w.must(plan)
		assert.Empty(t, placedReaders(w), "one reader up cannot read a pro card")
		w.s.ReaderStates["r2"] = ReaderUp
		plan, _ = TickAsk(w.s, TickReq{})
		w.must(plan)
		assert.ElementsMatch(t, []string{"r1", "r2"}, placedReaders(w))
	})
}
