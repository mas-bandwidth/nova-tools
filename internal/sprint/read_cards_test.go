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

// dealReads runs the tick's deal, which deals the read cards (TickDeal, withReadCards).
func dealReads(t *testing.T, w *world, seats []FriendSeat) Plan {
	t.Helper()
	return w.part(TickDeal, TickReq{Friends: seats})
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
	dealReads(t, w, nil)

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

	// dealt again, nothing more is cut: the reads stand
	dealReads(t, w, nil)
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

// readerSeat is a friend up of the tiers and roles given.
func readerSeat(name string, width int, tiers, roles []string) FriendSeat {
	return FriendSeat{Name: name, Width: width, Status: Up, Tiers: tiers, Roles: roles}
}

// TestAReadCardIsDealtToAReaderOfItsTierAndNeverTheWorker pins layer 2's rules, the deal's own
// (tier, roles) and a read's two: never the attempt's worker, never a reader that holds or
// closed a read of the attempt. A friend whose roles do not name reader is dealt no read
// (2026-10-06: builder-only friends got reads); a friend of flash is dealt no pro read; a
// member whose reader row is held reads nothing; the worker, friend or member, never.
func TestAReadCardIsDealtToAReaderOfItsTierAndNeverTheWorker(t *testing.T) {
	t.Parallel()
	t.Run("roles and tiers", func(t *testing.T) {
		t.Parallel()
		w := readCardsWorld(t, 4, "m1")
		putReviewBy(w, "s1-1", "s1-1: work (s1) tier: pro\n", "zoe", 1)
		seats := []FriendSeat{
			readerSeat("alex", 8, []string{"pro"}, []string{"builder"}),
			readerSeat("fred", 32, []string{"flash"}, []string{"builder", "reader"}),
			readerSeat("pat", 2, []string{"pro"}, []string{"reader"}),
			readerSeat("zoe", 2, []string{"pro"}, []string{"builder", "reader"}),
		}
		dealReads(t, w, seats)
		var rows []string
		for _, c := range readCardsOf(w, "s1-1") {
			rows = append(rows, c.Row)
		}
		require.ElementsMatch(t, []string{FriendRow("pat"), "m1"}, rows, "a reader of its tier, never a builder alone, never flash for pro, never zoe who worked it")
	})
	t.Run("the worker is a member", func(t *testing.T) {
		t.Parallel()
		w := readCardsWorld(t, 4, "m1", "m2")
		putReviewBy(w, "s1-1", "s1-1: work (s1)\n", "m1", 1)
		dealReads(t, w, nil)
		reads := readCardsOf(w, "s1-1")
		require.Len(t, reads, 1)
		require.Equal(t, "m2", reads[0].Row)
	})
	t.Run("a held reader row", func(t *testing.T) {
		t.Parallel()
		w := readCardsWorld(t, 4, "m1", "m2", "m3")
		w.s.ReaderStates = map[string]string{"reader-m2": ReaderHeld, "reader-m3": ReaderUp, "reader-m1": ReaderUp}
		putReviewBy(w, "s1-1", "s1-1: work (s1) tier: pro\n", "m1", 1)
		dealReads(t, w, nil)
		reads := readCardsOf(w, "s1-1")
		require.Len(t, reads, 1, "one reader of two: the other waits")
		require.Equal(t, "m3", reads[0].Row)
		askReaders(t, w, nil)
		require.Len(t, w.notesOf(NWaitingForReader), 1, "the read it lacks is said waiting")
	})
	t.Run("never twice to one reader at an attempt", func(t *testing.T) {
		t.Parallel()
		w := readCardsWorld(t, 4, "m1", "m2", "m3")
		putReviewBy(w, "s1-1", "s1-1: work (s1)\n", "m1", 1)
		dealReads(t, w, nil)
		first := readCardsOf(w, "s1-1")[0]
		// it hands the read back: spent, so the next goes to the other reader
		first.Fields["retired_by"] = RetiredByReturned
		w.place(w.s.Fleet, first.ID, "", "")
		dealReads(t, w, nil)
		next := readCardsOf(w, "s1-1")
		require.Len(t, next, 1)
		require.NotEqual(t, first.Row, next[0].Row)
	})
}

// TestAReadCostsHalfASlot pins the one width (the owner: "Go wide with reads, 2X regular
// width"): a row's load is its work cards and half its reads, so a member of width 2,
// holding DealAhead (2) widths, is dealt 8 reads and no more; with a work card on it, 6;
// the work of the same deal goes in the room the reads leave; and its take runs 2 work cards
// or 4 reads at once, its reads first.
func TestAReadCostsHalfASlot(t *testing.T) {
	t.Parallel()
	t.Run("the deal", func(t *testing.T) {
		t.Parallel()
		w := readCardsWorld(t, 2, "m1", "m2")
		for i := 1; i <= 10; i++ {
			putReviewBy(w, "s1-"+itoa(i), "s1-"+itoa(i)+": work (s1)\n", "m1", float64(i))
		}
		dealReads(t, w, nil)
		require.Equal(t, 8, w.s.Fleet.Count("m2", Ready), "8 reads fill 4 slots")
		require.Equal(t, 0, w.s.Fleet.Count("m1", Ready), "m1 worked them all")
		require.Equal(t, 4, memberLoads(w.s, []string{"m2"})["m2"])
	})
	t.Run("work in the room the reads leave", func(t *testing.T) {
		t.Parallel()
		w := readCardsWorld(t, 1, "m1", "m2")
		putReviewBy(w, "s1-1", "s1-1: work (s1)\n", "m1", 1)
		w.s.Work.SetRows([]string{"s1", "s2"})
		for i := 1; i <= 3; i++ {
			w.s.Work.Put(&Card{ID: "s2-" + itoa(i), Row: "s2", Col: Ready, Score: float64(10 + i), Rev: 1, Fields: map[string]string{"kind": "primary", "stream": "s2", "brief": "s2: work (s2)\n"}})
		}
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1", Width: 0}))
		dealReads(t, w, nil)
		work, reads := rowLoad(w.s, "m2")
		require.Equal(t, 1, reads)
		require.Equal(t, 1, work, "room 2 slots: one read (half), one work card; the half left takes no work")
	})
	t.Run("the take", func(t *testing.T) {
		t.Parallel()
		w := readCardsWorld(t, 2, "m1", "m2")
		for i := 1; i <= 6; i++ {
			putReviewBy(w, "s1-"+itoa(i), "s1-"+itoa(i)+": work (s1)\n", "m1", float64(i))
		}
		dealReads(t, w, nil)
		w.must(Take(w.s, TakeReq{As: "m2", Sel: Sel{Limit: -1}, Who: "m2"}))
		require.Equal(t, 4, w.s.Fleet.Count("m2", Working), "width 2 runs 4 reads at once")
		require.Equal(t, 2, w.s.Fleet.Count("m2", Ready))
	})
}
