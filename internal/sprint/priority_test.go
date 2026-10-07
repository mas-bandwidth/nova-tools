package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A recut's twin keeps its card's level as it keeps its tier (recut.go): a recut for a tier,
// and one for a new brief (the paths rule's and the widen rule's twins are such a recut),
// carry a hand-set level; a new brief that names its own PRIORITY line gives the twin that.
func TestARecutKeepsTheCardsPriority(t *testing.T) {
	t.Parallel()
	w := recutWorld(t)
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"old"}, Level: PriorityHigh, Reason: "the release waits on it", Who: "coordinator"}))

	w.must(Recut(w.s, RecutReq{ID: "old", Tier: "heavy", Who: "coordinator"}))
	tw := w.s.Work.Placed("oldb")
	require.NotNil(t, tw)
	assert.Equal(t, PriorityHigh, tw.F(FieldPriority), "a recut for a tier keeps the level")

	w.must(Recut(w.s, RecutReq{ID: "oldb", Brief: "tier: flash\nthe paths widened", Who: "coordinator"}))
	tc := w.s.Work.Placed("oldc")
	require.NotNil(t, tc)
	assert.Equal(t, PriorityHigh, tc.F(FieldPriority), "a recut for a brief (the paths rule's twin) keeps the level")

	w.must(Recut(w.s, RecutReq{ID: "oldc", New: "old-later", Brief: "tier: flash\nPRIORITY: low\nlater", Who: "coordinator"}))
	td := w.s.Work.Placed("old-later")
	require.NotNil(t, td)
	assert.Equal(t, PriorityLow, td.F(FieldPriority), "a new brief's own PRIORITY line is the twin's")

	// a card with no level set gives its twin none
	w.must(Recut(w.s, RecutReq{ID: "other", Tier: "heavy", Who: "coordinator"}))
	assert.Empty(t, w.s.Work.Placed("otherb").F(FieldPriority))
}

// A computed critical (CriticalBehind or more behind it, no level set) is shown apart, as
// critical by weight, and orders nothing yet: the deal and the ask keep it in stream turns.
func TestAComputedCriticalIsShownNotYetOrdered(t *testing.T) {
	t.Parallel()
	heavy := &Card{ID: "s1-2", Fields: map[string]string{"kind": "primary", FieldBehind: "12"}}
	plain := &Card{ID: "s1-1", Fields: map[string]string{"kind": "primary"}}
	l, src := CardPriority(heavy)
	assert.Equal(t, [2]string{PriorityCritical, "computed"}, [2]string{l, src})
	assert.Equal(t, []*Card{plain, heavy}, ladderOrder([]*Card{plain, heavy}), "not yet ordered: the given order stands")
	set := &Card{ID: "s1-3", Fields: map[string]string{"kind": "primary", FieldPriority: PriorityCritical}}
	assert.Equal(t, []*Card{set, plain, heavy}, ladderOrder([]*Card{plain, heavy, set}), "a critical set by hand is ordered")
	line := PriorityLine(map[string][]string{CriticalByWeight: {"s1-2"}, PriorityCritical: {"s1-3"}}, nil)
	assert.Equal(t, "priority: critical s1-3; critical (by weight, not yet ordered) s1-2", line)
}

// A read inherits its primary's level, the higher of reader and its primary's (the owner,
// 2026-10-06: "that work stream jumps to the front of the reader and merge queue"): a high
// primary's read card is dealt before a normal primary's older read, and every read before
// work, and it carries the level a reader's queue shows.
func TestAHighPrimarysReadIsAskedAndDealtFirst(t *testing.T) {
	t.Parallel()

	t.Run("a friend", func(t *testing.T) {
		t.Parallel()
		w, amy := priorityWorld(t, 5, 7, 0, 3)                     // her room is three slots, six reads
		w.s.Work.Card("s1-7").Fields[FieldPriority] = PriorityHigh // the newest in review
		for i := 1; i <= 3; i++ {
			w.s.Work.Card("s2-" + itoa(i)).Fields[FieldPriority] = PriorityHigh
		}
		tickDealAndAsk(t, w, amy)
		assert.ElementsMatch(t, []string{"s1-7", "s1-1", "s1-2", "s1-3", "s1-4", "s1-5"}, friendReads(w, "amy"), "the high read first, before the older normal reads; the last normal read waits")
		assert.Empty(t, friendNewWork(w, "amy"), "and before high work: the reads fill her room")
		rc := w.s.Fleet.Card(ReadCardID("s1-7", 1, "amy"))
		require.NotNil(t, rc)
		assert.Equal(t, PriorityHigh, QueuePriority(rc), "the read card carries its primary's level")
	})

	t.Run("the machines' ask", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-m1")
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1", Width: 1}))
		putReview(w, "s1-1", "s1-1: older (s1) tier: flash\n", 1, 1, "h1")
		w.s.Work.Card("s1-1").Fields[FieldFinishedAt] = stamp(t0.Add(-60 * 60e9))
		w.s.Work.Put(&Card{ID: "s1-2", Row: "s1", Col: Review, Score: 2, Rev: 1, Fields: map[string]string{
			"kind": "primary", "attempt": "1", "stream": "s1", "brief": "s1-2: high (s1) tier: flash\n", "head": "h2", FieldPriority: PriorityHigh}})
		askReaders(t, w, nil)
		rc := w.s.Fleet.Card(ReadCardID("s1-2", 1, "m1"))
		require.NotNil(t, rc, "the high primary's read card is the member's")
		assert.Equal(t, PriorityHigh, QueuePriority(rc), "it carries its primary's level")
		assert.NotEqual(t, PriorityHigh, QueuePriority(w.s.Fleet.Card(ReadCardID("s1-1", 1, "m1"))), "the normal read is at reader level")
	})
}

// A PRIORITY line that names no level is refused at admission and at a recut, naming the
// level found and the six of the ladder, never admitted silently at normal or the stream's
// default (nova-tools#5377's cold read: "PRIORITY: urgent" was taken as no line at all).
func TestAMisspelledPriorityIsRefused(t *testing.T) {
	t.Parallel()
	bad := "c: a card\nPRIORITY: urgent\n\nThe task."
	t.Run("add", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 0)
		p := Add(w.s, AddReq{Stream: "s9", Cards: []CardAdd{{ID: "s9-1", Brief: bad}}})
		require.Len(t, p.Refused, 1, "%+v", p)
		assert.Contains(t, p.Refused[0].Why, "found urgent")
		for _, l := range PriorityLadder {
			assert.Contains(t, p.Refused[0].Why, l)
		}
		assert.Nil(t, w.s.Work.Card("s9-1"))
	})
	t.Run("recut", func(t *testing.T) {
		t.Parallel()
		w := recutWorld(t)
		p := Recut(w.s, RecutReq{ID: "old", Brief: "tier: flash\nPRIORITY: urgent\nlater", Who: "coordinator"})
		require.Len(t, p.Refused, 1, "%+v", p)
		assert.Contains(t, p.Refused[0].Why, "found urgent")
		assert.Empty(t, p.Units)
		assert.Nil(t, w.s.Work.Card("oldb"))
	})
}
