package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A reader reads the tiers its row carries (readers.go ReaderReads; the owner,
// 2026-10-05, of a friend's flash lane: could it "do more reading?"):
// a flash reader is asked a flash card's read and never a pro card's, and a pro
// card with too few readers of its tier up is the few-readers judgment, never a
// read asked of a reader outside its tier (tla/ReaderTiers.tla, AskedWithinTier).

// tiersWorld is reader-fast reading flash only and reader-a and reader-b
// reading every tier, all up, with s1-1 a flash card and s1-2 a pro card on pro
// in review, unasked.
func tiersWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, "reader-fast", "reader-a", "reader-b")
	for _, m := range []string{"m1", "m2"} {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m}))
	}
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-2"}, Brief: proBrief}))
	w.s.Work.Card("s1-2").Fields[FieldTierNow] = "pro"
	toReview(w, "s1-1", "s1-2")
	w.s.ReaderStates = map[string]string{"reader-fast": ReaderUp, "reader-a": ReaderUp, "reader-b": ReaderUp}
	w.s.ReaderTiers = map[string][]string{"reader-fast": {"flash"}}
	return w
}

// readersOf is every reader asked a read of the primary, at any attempt, placed or retired.
func readersOf(w *world, id string) []string { return readerNames(w.s.Readers.Of(id)) }

func TestTheAskNeverAsksAReaderOutsideItsTiers(t *testing.T) {
	t.Parallel()
	t.Run("a reader's tiers", func(t *testing.T) {
		t.Parallel()
		w := tiersWorld(t)
		assert.True(t, w.s.ReaderReads("reader-fast", "flash"))
		assert.False(t, w.s.ReaderReads("reader-fast", "pro"))
		assert.False(t, w.s.ReaderReads("reader-fast", "heavy"))
		for _, tier := range []string{"flash", "pro", "heavy"} {
			assert.True(t, w.s.ReaderReads("reader-a", tier), "a row with no tiers reads every tier (today's behaviour)")
		}
	})
	t.Run("a flash card may be asked of either", func(t *testing.T) {
		t.Parallel()
		w := tiersWorld(t)
		fl, pro := w.s.Work.Card("s1-1"), w.s.Work.Card("s1-2")
		assert.ElementsMatch(t, []string{"reader-fast", "reader-a", "reader-b"}, w.s.freeReaders(fl, 1))
		assert.ElementsMatch(t, []string{"reader-a", "reader-b"}, w.s.freeReaders(pro, 1))
		// with only the flash reader free of room elsewhere, the flash card goes to it
		w.s.ReaderStates["reader-a"], w.s.ReaderStates["reader-b"] = ReaderDown, ReaderDown
		plan, _ := TickAsk(w.s, TickReq{})
		w.must(plan)
		assert.Equal(t, []string{"reader-fast"}, readersOf(w, "s1-1"), "a flash card is asked of the flash reader")
		assert.Empty(t, readersOf(w, "s1-2"), "a pro card is never asked of the flash reader")
	})
	t.Run("a pro card is asked only of all-tier readers", func(t *testing.T) {
		t.Parallel()
		w := tiersWorld(t)
		for i := 0; i < 4; i++ {
			plan, _ := TickAsk(w.s, TickReq{})
			w.must(plan)
			for _, rc := range w.s.Readers.Of("s1-2") {
				if rc.Col == Asked && rc.Placed() {
					w.must(Read(w.s, ReadReq{As: rc.Row, Verdict: "ok", Sel: Sel{IDs: []string{rc.ID}}}))
				}
			}
		}
		assert.ElementsMatch(t, []string{"reader-a", "reader-b"}, readersOf(w, "s1-2"), "the pro card's two reads, both by pro readers")
		assert.True(t, acceptable(w.s, w.s.Work.Card("s1-2")))
		// and Ask by hand the same: --another has no pro reader left to ask
		p := Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-2"}}, Another: true})
		require.Len(t, p.Refused, 1)
		assert.NotContains(t, readersOf(w, "s1-2"), "reader-fast")
	})
	t.Run("one pro reader up is the few-readers judgment", func(t *testing.T) {
		t.Parallel()
		w := tiersWorld(t)
		w.s.ReaderStates["reader-b"] = ReaderDown
		pro := w.s.Work.Card("s1-2")
		assert.False(t, enoughReadersUp(w.s, pro), "one pro reader up for a card that needs two")
		assert.True(t, enoughReadersUp(w.s, w.s.Work.Card("s1-1")))
		plan, _ := TickAsk(w.s, TickReq{})
		var few []Note
		for _, n := range plan.Notes {
			if n.Type == NFewReaders {
				few = append(few, n)
			}
		}
		require.Len(t, few, 1, "the existing judgment: %+v", plan.Notes)
		assert.Contains(t, few[0].What, "reader-fast up (reads flash)", "the judgment names the tiers a reader reads")
		w.must(plan)
		assert.Empty(t, readersOf(w, "s1-2"), "nothing is asked of the pro card")
		assert.Len(t, readersOf(w, "s1-1"), 1, "the flash card is asked")
		for _, rc := range w.s.Readers.Cell("reader-fast", Asked) {
			assert.Equal(t, "s1-1", rc.F("primary"), "the flash reader is asked flash only")
		}
	})
	t.Run("a returned read is never re-asked in place outside its tier", func(t *testing.T) {
		t.Parallel()
		w := tiersWorld(t)
		// the pro card's first read ok by reader-a; its second asked of the flash
		// reader before its row carried tiers, and handed back
		w.s.ReaderTiers = nil
		w.s.ReaderStates["reader-b"] = ReaderDown
		w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-2"}}}))
		first := askedRead(w, "s1-2")
		if first.Row != "reader-a" {
			w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-2"}}, Instead: first.Row}))
			first = askedRead(w, "s1-2")
		}
		require.Equal(t, "reader-a", first.Row)
		w.must(Read(w.s, ReadReq{As: first.Row, Verdict: "ok", Sel: Sel{IDs: []string{first.ID}}}))
		w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-2"}}}))
		rc := askedRead(w, "s1-2")
		require.Equal(t, "reader-fast", rc.Row)
		w.must(Read(w.s, ReadReq{As: rc.Row, Return: true, Reason: "too hard", Sel: Sel{IDs: []string{rc.ID}}}))
		// tiers declared: one pro reader up, so the pro card is judged, and the
		// return is not asked again of fast in place
		w.s.ReaderTiers = map[string][]string{"reader-fast": {"flash"}}
		plan, _ := TickAsk(w.s, TickReq{})
		w.must(plan)
		assert.NotEmpty(t, w.s.Readers.Card(rc.ID).F(FieldReturned), "the flash reader's returned pro read is not re-asked in place")
		p := Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-2"}}})
		require.Len(t, p.Refused, 1, "by hand too: no reader of its tier is free")
		// a pro reader back: the returned read goes to it, the fast reader's card retired
		w.s.ReaderStates["reader-b"] = ReaderUp
		plan, _ = TickAsk(w.s, TickReq{})
		w.must(plan)
		assert.False(t, w.s.Readers.Card(rc.ID).Placed(), "retired, never re-asked in place")
		assert.Equal(t, "reader-b", askedRead(w, "s1-2").Row)
	})
	t.Run("the level never moves a read outside the reader's tiers", func(t *testing.T) {
		t.Parallel()
		w := tiersWorld(t)
		w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-3", "s1-4"}, Brief: proBrief}))
		for _, id := range []string{"s1-3", "s1-4"} {
			w.s.Work.Card(id).Fields[FieldTierNow] = "pro"
		}
		toReview(w, "s1-3", "s1-4")
		// three pro reads piled on reader-a, reader-b down: fast is idle but reads no pro
		w.s.ReaderStates["reader-b"] = ReaderDown
		for _, id := range []string{"s1-2", "s1-3", "s1-4"} {
			pr := w.s.Work.Card(id)
			fields := map[string]string{"kind": "read", "primary": id, "stream": "s1", "reader": "reader-a", "attempt": "1", "head": pr.F("head"), "asked": stamp(w.s.Now)}
			w.must(Plan{Units: []Unit{{Key: id, Stream: "s1", Changes: []Change{change(Readers, createEntry(ReadCardID(id, 1, "reader-a"), "reader-a", Asked, pr.Score, fields))}}}})
		}
		require.Len(t, w.s.Readers.Cell("reader-a", Asked), 3)
		plan, _ := TickLevelReads(w.s, TickReq{})
		w.must(plan)
		assert.Empty(t, w.s.Readers.Cell("reader-fast", Asked), "the level moves no pro read to the flash reader")
		assert.Len(t, w.s.Readers.Cell("reader-a", Asked), 3)
		// without tiers the same pile is levelled onto fast: the level runs, the tier stops it
		w.s.ReaderTiers = nil
		plan, _ = TickLevelReads(w.s, TickReq{})
		assert.NotEmpty(t, plan.Units, "with every reader reading every tier the level moves a read")
	})
}
