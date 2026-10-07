package sprint

import (
	"strings"
	"testing"
	"time"

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
		require.Equal(t, "1", c.F(FieldReadCard))
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

// TestTurningReadCardsOnNeverReadsAPrimaryTwice pins the switch on a live store: a primary
// whose reads were asked the old way keeps a read begun on the readers table (it finishes
// there and stands), has its read asked and not begun taken back (retired by read cards),
// and is dealt read cards only for the reads it still needs, never to the machine whose
// reader holds its read nor to its worker.
func TestTurningReadCardsOnNeverReadsAPrimaryTwice(t *testing.T) {
	t.Parallel()
	w := readCardsWorld(t, 4, "m1", "m2", "m3", "m4")
	putReviewBy(w, "s1-1", "s1-1: work (s1) tier: pro\n", "m1", 1)
	old := func(reader, col string) string {
		id := ReadCardID("s1-1", 1, reader)
		w.s.Readers.Put(&Card{ID: id, Row: reader, Col: col, Score: 1, Rev: 1, Fields: map[string]string{
			"kind": "read", "primary": "s1-1", "stream": "s1", "reader": reader, "attempt": "1", "head": "head-s1-1"}})
		return id
	}
	begun := old("reader-m2", Reading)
	asked := old("reader-m3", Asked)
	dealReads(t, w, nil)
	require.True(t, w.s.Readers.Card(begun).Placed(), "a read begun the old way finishes there")
	require.False(t, w.s.Readers.Card(asked).Placed(), "a read asked and not begun is taken back")
	require.Equal(t, RetiredByCards, w.s.Readers.Card(asked).F("retired_by"))
	reads := readCardsOf(w, "s1-1")
	require.Len(t, reads, 1, "one read card: the read begun stands for the other")
	require.NotContains(t, []string{"m1", "m2"}, reads[0].Row, "never its worker, never the machine reading it the old way")
	dealReads(t, w, nil)
	require.Len(t, readCardsOf(w, "s1-1"), 1)
}

// TestAReadCardsDeadlineRunsFromItsStart pins PR 5392's cold read, finding 2: a read card's
// deadline runs from its take, never from the deal, so a read that waits in a member's ready
// queue behind long work is not late and spends no one; one that waits past the deal bound
// (DealtMax) untouched is taken back spending nothing (unstarted), and its reader may be dealt
// it again; one taken and not closed within ReadCardDeadline is late and spends its reader.
func TestAReadCardsDeadlineRunsFromItsStart(t *testing.T) {
	t.Parallel()
	w := readCardsWorld(t, 4, "m1", "m2")
	putReviewBy(w, "s1-1", "s1-1: work (s1)\n", "m1", 1)
	dealReads(t, w, nil)
	rc := readCardsOf(w, "s1-1")[0]
	require.Equal(t, "m2", rc.Row)
	w.tick(ReadCardDeadline + time.Minute)
	dealReads(t, w, nil)
	require.True(t, w.s.Fleet.Card(rc.ID).Placed(), "never taken: not late")

	w.tick(w.s.DealtMax())
	dealReads(t, w, nil)
	require.Equal(t, RetiredByUnstarted, w.s.Fleet.Card(rc.ID).F("retired_by"), "untouched past the deal bound: taken back")
	again := readCardsOf(w, "s1-1")
	require.Len(t, again, 1, "dealt again, to the reader it spent nothing of")
	require.Equal(t, rc.ID+".g1", again[0].ID)

	w.must(Take(w.s, TakeReq{As: "m2", Sel: Sel{Limit: -1}, Who: "m2"}))
	w.tick(ReadCardDeadline - time.Minute)
	dealReads(t, w, nil)
	require.True(t, w.s.Fleet.Card(again[0].ID).Placed(), "within its deadline from the take")
	w.tick(2 * time.Minute)
	dealReads(t, w, nil)
	require.Equal(t, RetiredByLate, w.s.Fleet.Card(again[0].ID).F("retired_by"), "taken and not closed in time: late, spent")
	require.Empty(t, readCardsOf(w, "s1-1"), "m2 spent it and m1 worked it: no reader left, it waits")
}

// TestTheWorkSwitchesStopWorkNeverReads pins read cards beside set --fleet off and
// --friends off (PR 5395): the switches stop the work dealt, and a machine's reader and a
// friend who reads are still dealt read cards.
func TestTheWorkSwitchesStopWorkNeverReads(t *testing.T) {
	t.Parallel()
	w := readCardsWorld(t, 4, "m1", "m2")
	w.s.Work.SetProp(PropFleet, "off")
	w.s.Work.SetProp(PropFriends, "off")
	putReviewBy(w, "s1-1", "s1-1: work (s1) tier: pro\n", "m1", 1)
	dealReads(t, w, []FriendSeat{readerSeat("pat", 2, []string{"pro"}, []string{"reader"})})
	var rows []string
	for _, c := range readCardsOf(w, "s1-1") {
		rows = append(rows, c.Row)
	}
	require.ElementsMatch(t, []string{"m2", FriendRow("pat")}, rows)
}

// TestAFriendReadsEveryTierAtOrBelowHers pins the read's tier rule on a friend (the live
// sprint of 2026-10-06 8:32 PM ET, where no read card was cut): a friend reads a card of
// her tier or any tier below it, as the friends' reads always did (friendAtOrAbove), so a
// heavy friend reads a flash and a pro card.
func TestAFriendReadsEveryTierAtOrBelowHers(t *testing.T) {
	t.Parallel()
	w := readCardsWorld(t, 4)
	putReviewBy(w, "s1-1", "s1-1: work (s1)\n", "fred", 1)
	putReviewBy(w, "s1-2", "s1-2: work (s1) tier: pro\n", "fred", 2)
	putReviewBy(w, "s1-3", "s1-3: work (s1) tier: pro\n", "zoe", 3)
	seats := []FriendSeat{
		readerSeat("jon", 16, []string{"heavy"}, []string{"builder", "reader"}),
		readerSeat("fred", 32, []string{"flash"}, []string{"builder", "reader"}),
	}
	dealReads(t, w, seats)
	rows := func(id string) []string {
		var out []string
		for _, c := range readCardsOf(w, id) {
			out = append(out, c.Row)
		}
		return out
	}
	require.Equal(t, []string{FriendRow("jon")}, rows("s1-1"), "heavy reads flash")
	require.Equal(t, []string{FriendRow("jon")}, rows("s1-2"), "heavy reads pro; fred worked it")
	require.ElementsMatch(t, []string{FriendRow("jon"), FriendRow("fred")}, rows("s1-3"), "a flash friend reads a pro card one tier below it (TestAReaderOneTierBelowMayTakeAReadCard)")
}

// TestAReaderOneTierBelowMayTakeAReadCard pins the owner's interim read rule for read cards
// (2026-10-06 7:11 PM ET "let flash read pro", 7:41 PM "let pro do it"): a reader one tier
// below a card's read tier may take its read card, never two below. A pro card whose heavy
// friend is full has its second read dealt to the flash friend; with every reader idle, its
// own tier first, then one below, before the tier above
// (TestAReadCardGoesToTheCheapestReaderThatMayTakeIt).
func TestAReaderOneTierBelowMayTakeAReadCard(t *testing.T) {
	t.Parallel()
	t.Run("the heavy friend full", func(t *testing.T) {
		t.Parallel()
		w := readCardsWorld(t, 4)
		putReviewBy(w, "s1-1", "s1-1: work (s1) tier: pro\n", "zoe", 1)
		// jon holds one read of it and has no room for more
		jon := readerSeat("jon", 1, []string{"heavy"}, []string{"builder", "reader"})
		w.s.Fleet.SetRows(append(w.s.Fleet.Rows(), FriendRow("jon")))
		w.s.Fleet.Put(&Card{ID: ReadCardID("s1-1", 1, "jon"), Row: FriendRow("jon"), Col: Working, Score: 1, Rev: 1, Fields: map[string]string{
			"kind": "read", "primary": "s1-1", "stream": "s1", "reader": "jon", "attempt": "1", "head": "work-s1-1", FieldReadCard: "1", "asked": stamp(t0)}})
		for i := 0; i < 3; i++ {
			w.s.Fleet.Put(&Card{ID: "busy-" + itoa(i) + ".w1", Row: FriendRow("jon"), Col: Working, Score: 1, Rev: 1, Fields: map[string]string{"kind": "work", "primary": "busy-" + itoa(i), "stream": "s9", "attempt": "1"}})
		}
		fred := readerSeat("fred", 32, []string{"flash"}, []string{"builder", "reader"})
		dealReads(t, w, []FriendSeat{jon, fred})
		var rows []string
		for _, c := range readCardsOf(w, "s1-1") {
			rows = append(rows, c.Row)
		}
		require.ElementsMatch(t, []string{FriendRow("jon"), FriendRow("fred")}, rows, "the second read goes to the flash friend")
	})
	t.Run("its own tier, then one below, before above", func(t *testing.T) {
		t.Parallel()
		w := readCardsWorld(t, 4)
		putReviewBy(w, "s1-1", "s1-1: work (s1) tier: pro\n", "zoe", 1)
		dealReads(t, w, []FriendSeat{
			readerSeat("fred", 32, []string{"flash"}, []string{"builder", "reader"}),
			readerSeat("jon", 16, []string{"heavy"}, []string{"builder", "reader"}),
			readerSeat("pat", 2, []string{"pro"}, []string{"reader"}),
		})
		var rows []string
		for _, c := range readCardsOf(w, "s1-1") {
			rows = append(rows, c.Row)
		}
		require.ElementsMatch(t, []string{FriendRow("pat"), FriendRow("fred")}, rows, "the pro reader, then the flash reader one below; never the heavy reader while they are idle")
	})
	t.Run("two tiers below never", func(t *testing.T) {
		t.Parallel()
		w := readCardsWorld(t, 4)
		putReviewBy(w, "s1-1", "s1-1: work (s1) tier: heavy\n", "zoe", 1)
		dealReads(t, w, []FriendSeat{readerSeat("fred", 32, []string{"flash"}, []string{"reader"})})
		require.Empty(t, readCardsOf(w, "s1-1"), "flash never reads heavy")
	})
}

// busyFriend puts n work cards working on the friend's row: at width n she has no idle lane.
func busyFriend(w *world, name string, n int) {
	row := FriendRow(name)
	if !w.s.Fleet.HasRow(row) {
		w.s.Fleet.SetRows(append(w.s.Fleet.Rows(), row))
	}
	for i := 0; i < n; i++ {
		id := "busy-" + name + "-" + itoa(i)
		w.s.Fleet.Put(&Card{ID: id + ".w1", Row: row, Col: Working, Score: 1, Rev: 1, Fields: map[string]string{"kind": "work", "primary": id, "stream": "s9", "attempt": "1"}})
	}
}

// TestAReadCardGoesToTheCheapestReaderThatMayTakeIt pins the choice of reader (the owner,
// 2026-10-06: a read's tier is its primary's; "putting a pro task on a heavy model is a
// waste"; the flash reader is the widest, fastest and cheapest and takes the flash reads;
// reads are dealt in parallel, never stacked behind one reader). Of the readers that may
// take a read (mayReadCard), one with an idle lane first; among them the read's own tier,
// then one tier below, then the tiers above, nearest first; within a tier the most idle
// lanes, then the most room, then by name. Friends and members are ordered together.
func TestAReadCardGoesToTheCheapestReaderThatMayTakeIt(t *testing.T) {
	t.Parallel()
	fred := func(width int) FriendSeat { return readerSeat("fred", width, []string{"flash"}, []string{"reader"}) }
	pat := func(width int) FriendSeat { return readerSeat("pat", width, []string{"pro"}, []string{"reader"}) }
	jon := func(width int) FriendSeat { return readerSeat("jon", width, []string{"heavy"}, []string{"reader"}) }
	cases := []struct {
		name    string
		brief   string
		members []string
		seats   []FriendSeat
		busy    map[string]int
		holds   string // a reader that already holds a read of the attempt
		tiers   string // the members' reader rows' tiers (reader set --tiers)
		want    []string
	}{
		{name: "a flash read goes to the flash reader", brief: "tier: flash",
			seats: []FriendSeat{fred(4), pat(8), jon(16)}, want: []string{FriendRow("fred")}},
		{name: "a pro read with the pro reader busy goes to the flash reader", brief: "tier: pro",
			seats: []FriendSeat{fred(4), pat(1), jon(16), readerSeat("kim", 4, []string{"pro"}, []string{"reader"})},
			busy:  map[string]int{"pat": 1}, holds: "kim", want: []string{FriendRow("fred")}},
		{name: "a heavy read goes to the heavy reader, then one tier below", brief: "tier: heavy",
			seats: []FriendSeat{fred(32), pat(4), jon(4), readerSeat("ada", 16, []string{"frontier"}, []string{"reader"})},
			want:  []string{FriendRow("jon"), FriendRow("pat")}},
		{name: "a flash read with every flash and pro reader busy goes to the heavy reader", brief: "tier: flash",
			seats: []FriendSeat{fred(1), pat(1), jon(16)}, busy: map[string]int{"fred": 1, "pat": 1},
			want: []string{FriendRow("jon")}},
		{name: "two reads of one primary go to two idle readers of its tier", brief: "tier: pro",
			seats: []FriendSeat{pat(4), readerSeat("kim", 4, []string{"pro"}, []string{"reader"}), jon(16)},
			want:  []string{FriendRow("pat"), FriendRow("kim")}},
		{name: "a frontier read goes to the frontier reader, then the heavy one, before pro members", brief: "tier: frontier",
			members: []string{"m1", "m2"}, tiers: "pro",
			seats: []FriendSeat{readerSeat("ada", 16, []string{"frontier"}, []string{"reader"}), jon(16)},
			want:  []string{FriendRow("ada"), FriendRow("jon")}},
		{name: "a heavy read: a pro member reads one below, as a pro friend does", brief: "tier: heavy",
			members: []string{"m1"}, tiers: "pro",
			seats: []FriendSeat{jon(16), pat(8)},
			want:  []string{FriendRow("jon"), FriendRow("pat")}},
		{name: "a member idle before a friend of its tier busy", brief: "tier: flash", members: []string{"m1"},
			seats: []FriendSeat{fred(1)}, busy: map[string]int{"fred": 1}, want: []string{"m1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := readCardsWorld(t, 4, tc.members...)
			putReviewBy(w, "s1-1", "s1-1: work (s1) "+tc.brief+"\n", "zoe", 1)
			if tc.tiers != "" {
				w.s.Readers.Texts = map[string]map[string]string{}
				for _, m := range tc.members {
					w.s.Readers.Texts[ReaderPrefix+m] = map[string]string{ReaderTiers: tc.tiers}
				}
			}
			for name, n := range tc.busy {
				busyFriend(w, name, n)
			}
			if tc.holds != "" {
				row := FriendRow(tc.holds)
				w.s.Fleet.SetRows(append(w.s.Fleet.Rows(), row))
				w.s.Fleet.Put(&Card{ID: ReadCardID("s1-1", 1, tc.holds), Row: row, Col: Working, Score: 1, Rev: 1, Fields: map[string]string{
					"kind": "read", "primary": "s1-1", "stream": "s1", "reader": tc.holds, "attempt": "1", "head": "work-s1-1", FieldReadCard: "1", "asked": stamp(t0)}})
			}
			dealReads(t, w, tc.seats)
			var rows []string
			for _, c := range readCardsOf(w, "s1-1") {
				if c.F("reader") != tc.holds {
					rows = append(rows, c.Row)
				}
			}
			require.ElementsMatch(t, tc.want, rows)
		})
	}
}

// TestAWithdrawnReadCardSpendsNoOne pins the 2026-10-06 evening finding: a read card the
// machine withdrew (FriendTake for a friend's hold or stall: "a read withdrawn is history",
// check.go) spends nothing of its reader's, so the reader is dealt the read again at the
// attempt, at the next of its identities. Counted spent, a flash friend of width 32 held 1
// read while 15 pro reads waited on her alone.
func TestAWithdrawnReadCardSpendsNoOne(t *testing.T) {
	t.Parallel()
	w := readCardsWorld(t, 4)
	putReviewBy(w, "s1-1", "s1-1: work (s1)\n", "bob", 1)
	seats := []FriendSeat{readerSeat("amy", 8, []string{"flash"}, []string{"builder", "reader"})}
	id := ReadCardID("s1-1", 1, "amy")
	w.s.Fleet.Put(&Card{ID: id, Row: FriendRow("amy"), Col: Withdrawn, Score: 1, Rev: 1, Fields: map[string]string{
		"kind": "read", "primary": "s1-1", "stream": "s1", "reader": "amy", "attempt": "1", "head": "work-s1-1", FieldReadCard: "1"}})
	dealReads(t, w, seats)
	reads := readCardsOf(w, "s1-1")
	var live []string
	for _, c := range reads {
		if c.Col != Withdrawn {
			live = append(live, c.ID)
		}
	}
	require.Equal(t, []string{id + ".g1"}, live, "the withdrawn read is history: its reader is dealt it again")
}

// TestReadCardsWhySaysWhichClauseRefusesEachUnit pins tick --shadow's account of the read
// ask (ReadCardsWhy): a primary no unit may read gets a line with its tier, its wants, why it
// waits and each unit with the clause of mayReadCard that refuses it.
func TestReadCardsWhySaysWhichClauseRefusesEachUnit(t *testing.T) {
	t.Parallel()
	w := readCardsWorld(t, 4)
	putReviewBy(w, "s1-1", "s1-1: work (s1) tier: heavy\n", "bob", 1)
	seats := []FriendSeat{
		readerSeat("amy", 8, []string{"flash"}, []string{"builder", "reader"}),
		readerSeat("bob", 8, []string{"heavy"}, []string{"builder", "reader"}),
	}
	why := ReadCardsWhy(w.s, seats)
	require.Len(t, why, 2, "the units, then the one primary that waits")
	require.Contains(t, why[0], "units: amy half=")
	require.Equal(t, "s1-1 tier=heavy wants=2 waits: no reader up may read it: no friend whose tiers reach its read tier, and no member whose reader row serves its tier, besides its own worker; refused: amy=friend tier, bob=worker", why[1])
	w.s.Work.SetProp(PropReadCards, "off")
	require.Empty(t, ReadCardsWhy(w.s, seats), "nothing with read cards off")
}

// TestAReadHandedBackByFriendTakeIsNotDealtToHerAgain pins PR 5407's cold read: a read card
// the seat or her runner handed back (friend take: her runner asks it back when she judges
// it outside her tiers) spends her, as read --return does, so the deal never re-deals her
// the same read at the attempt; the machine's take-back (a hold) still spends nothing.
func TestAReadHandedBackByFriendTakeIsNotDealtToHerAgain(t *testing.T) {
	t.Parallel()
	for _, hold := range []bool{false, true} {
		w := readCardsWorld(t, 4)
		putReviewBy(w, "s1-1", "s1-1: work (s1)\n", "bob", 1)
		seats := []FriendSeat{readerSeat("amy", 8, []string{"flash"}, []string{"builder", "reader"})}
		dealReads(t, w, seats)
		id := ReadCardID("s1-1", 1, "amy")
		require.NotNil(t, w.s.Fleet.Placed(id))
		w.must(FriendTake(w.s, FriendTakeReq{Friend: "amy", IDs: []string{id}, All: hold, Hold: hold, Spends: !hold, Reason: "outside her tiers", Who: "coordinator"}))
		require.Equal(t, Withdrawn, w.s.Fleet.Card(id).Col)
		dealReads(t, w, seats)
		if hold {
			require.NotNil(t, w.s.Fleet.Placed(id+".g1"), "a hold's take-back spends nothing: dealt again")
			continue
		}
		require.Nil(t, w.s.Fleet.Placed(id+".g1"), "a read she handed back is never dealt to her again at the attempt")
	}
}

// TestAReadCardHoldsItsPrimaryToTheReadCardsDeadline pins the 2026-10-06 10:18 PM finding:
// a read card working on a friend's row holds its primary in review until the read card's
// own deadline (ReadCardDeadline from its start, when the ask takes it back late), never
// only the old friend read's DeadlineUnbegun from its ask; eight primaries with a read
// working were judged stalled ("no read is outstanding") 30 minutes after the ask.
func TestAReadCardHoldsItsPrimaryToTheReadCardsDeadline(t *testing.T) {
	t.Parallel()
	w := readCardsWorld(t, 4)
	putReviewBy(w, "s1-1", "s1-1: work (s1)\n", "bob", 1)
	dealReads(t, w, []FriendSeat{readerSeat("amy", 8, []string{"flash"}, []string{"builder", "reader"})})
	rc := w.s.Fleet.Placed(ReadCardID("s1-1", 1, "amy"))
	require.NotNil(t, rc)
	require.Equal(t, Working, rc.Col)
	w.s.Now = w.s.Now.Add(DeadlineUnbegun + 15*time.Minute)
	hd := mustHold(t, running(w), "s1-1", HeldByActor)
	require.Contains(t, hd.Why, rc.ID)
	w.s.Now = w.s.Now.Add(ReadCardDeadline)
	require.NotEqual(t, HeldByActor, Holder(running(w), w.s.Now, "s1-1").By, "past the read card's deadline it holds nothing")
}

// TestReadCardsWhySaysWhyAPrimaryWantsNoRead pins tick --shadow's wants-0 line: a primary in
// review, not acceptable, that wants no read card says why (failed work, or the reads that
// stand, each named).
func TestReadCardsWhySaysWhyAPrimaryWantsNoRead(t *testing.T) {
	t.Parallel()
	w := readCardsWorld(t, 4)
	putReviewBy(w, "s1-1", "s1-1: work (s1)\n", "bob", 1)
	putReviewBy(w, "s1-2", "s1-2: work (s1)\n", "bob", 2)
	w.s.Work.Card("s1-2").Fields["result"] = "failed"
	seats := []FriendSeat{readerSeat("amy", 8, []string{"flash"}, []string{"builder", "reader"})}
	dealReads(t, w, seats)
	why := strings.Join(ReadCardsWhy(w.s, seats), "\n")
	require.Contains(t, why, "s1-1 wants 0: attempt=1 stands: placed "+ReadCardID("s1-1", 1, "amy")+" col=working")
	require.Contains(t, why, "s1-2 wants 0: its work came back failed (result=failed)")
}
