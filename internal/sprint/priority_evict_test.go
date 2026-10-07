package sprint

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// evictWorld is one member m1 at width 3, at its room: s1-1, s1-2 and s1-3 working (taken
// 10, 2 and 20 minutes ago), s1-4, s1-5 and s1-6 ready behind them, never taken.
func evictWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, "reader-a", "reader-b")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1", Width: 3}))
	w.must(Add(w.s, AddReq{Brief: "a card", Stream: "s1", Count: 6}))
	w.must(Deal(w.s, DealReq{Sel: Sel{Limit: 6}}))
	require.Equal(t, 6, w.s.Fleet.Count("m1", Ready))
	w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{Limit: 3}}))
	require.Equal(t, 3, w.s.Fleet.Count("m1", Working))
	for id, ago := range map[string]time.Duration{"s1-1": 10 * time.Minute, "s1-2": 2 * time.Minute, "s1-3": 20 * time.Minute} {
		wc := w.s.Fleet.Card(WorkCardID(id, 1))
		require.Equal(t, Working, wc.Col, id)
		wc.Fields["taken"], wc.Fields["first_taken"] = stamp(t0.Add(-ago)), stamp(t0.Add(-ago))
	}
	return w
}

// addBlocker admits the blocker b-1 to stream b, with the WHO line given (none when "").
func addBlocker(w *world, who string) {
	w.t.Helper()
	brief := "b-1: the block\nREPO: mas-bandwidth/nova-tools\nPRIORITY: blocker\n"
	if who != "" {
		brief += "WHO: " + who + "\n"
	}
	w.must(Add(w.s, AddReq{Stream: "b", Cards: []CardAdd{{ID: "b-1", Brief: brief + "\nStop everything."}}}))
	l, src := CardPriority(w.s.Work.Placed("b-1"))
	require.Equal(w.t, [2]string{PriorityBlocker, "set"}, [2]string{l, src}, "the brief's line seeds blocker")
}

// A blocker is stored, dealt first, and evicts (docs/SPEC-SPRINT.md section 1, "Priority";
// tla/Priority.tla; the owner, 2026-10-06): on a row at its room, a ready blocker evicts the
// lowest level first, then among equals the one running the shortest, never a blocker; the
// evicted card goes back to ready at its level, its lane ends by the generation (a finish at
// the old one is stale), the record says "evicted by <blocker>", the room takes the blocker on
// the tick that follows, and the member's take takes it first. With only blockers running
// nothing is evicted and the blocker waits under one judgment.
func TestABlockerIsStoredAndEvictsTheLowestThenTheShortest(t *testing.T) {
	t.Parallel()

	t.Run("the low card first", func(t *testing.T) {
		t.Parallel()
		w := evictWorld(t)
		w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-1"}, Level: PriorityLow, Reason: "background", Who: "coordinator"}))
		addBlocker(w, "")
		p, due := TickDeal(w.s, TickReq{})
		w.must(p)
		assert.Positive(t, due, "the eviction is due work: the next tick follows at once")
		wc := w.s.Fleet.Card("s1-1.w1")
		require.Equal(t, Withdrawn, wc.Col, "the low card, 10 minutes in, is evicted before the normal one running 2 minutes")
		assert.Equal(t, "evicted by b-1", wc.F(FieldTakenBack))
		assert.Equal(t, "1", wc.F(FieldCarryGen), "the generation whose branch holds its pushed work is carried")
		assert.Equal(t, 2, wc.Int("gen"), "withdrawn at a new generation: the member reaps the claim that moved")
		assert.Equal(t, Working, w.s.Fleet.Card("s1-2.w1").Col)
		assert.Equal(t, Working, w.s.Fleet.Card("s1-3.w1").Col)
		pr := w.s.Work.Placed("s1-1")
		assert.Equal(t, Ready, pr.Col, "its primary goes back to ready")
		l, _ := CardPriority(pr)
		assert.Equal(t, PriorityLow, l, "at its own level")
		notes := w.notesOf(NEvicted)
		require.Len(t, notes, 1)
		assert.Contains(t, notes[0].What, "evicted by b-1: s1-1.w1 (low, running 10m0s)")
		assert.Equal(t, []string{"s1-1"}, notes[0].Primaries, "on the evicted primary's timeline")
		assert.Equal(t, Ready, w.s.Work.Placed("b-1").Col, "the blocker waits this tick for the room the eviction frees")

		// the member's lane: a finish at the old generation is stale (the member reaps it)
		fin := Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: map[string]int{"s1-1.w1": 1}, As: "m1"})
		require.Len(t, fin.Refused, 1)
		assert.Contains(t, fin.Refused[0].Why, "stale")

		// the tick that follows deals the blocker into the room, first by the ladder
		p, _ = TickDeal(w.s, TickReq{})
		w.must(p)
		b := w.s.Fleet.Card("b-1.w1")
		require.NotNil(t, b, "the blocker is dealt")
		assert.Equal(t, [2]string{"m1", Ready}, [2]string{b.Row, b.Col})
		assert.Equal(t, PriorityBlocker, QueuePriority(b), "the work card carries the level")
		assert.Equal(t, Working, w.s.Work.Placed("b-1").Col)
		assert.Equal(t, Ready, w.s.Work.Placed("s1-1").Col, "the evicted card waits for room at its level")
		// and the member's take takes the blocker before the three ready normals dealt earlier
		w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{Limit: 1}}))
		assert.Equal(t, Working, w.s.Fleet.Card("b-1.w1").Col, "the blocker is taken first")
		assert.Equal(t, Ready, w.s.Fleet.Card("s1-4.w1").Col)
	})

	t.Run("with no low card, the shortest running", func(t *testing.T) {
		t.Parallel()
		w := evictWorld(t)
		addBlocker(w, "")
		p, _ := TickDeal(w.s, TickReq{})
		w.must(p)
		assert.Equal(t, Withdrawn, w.s.Fleet.Card("s1-2.w1").Col, "the normal running 2 minutes, not the ones at 10 and 20")
		assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)
		assert.Equal(t, Working, w.s.Fleet.Card("s1-3.w1").Col)
		assert.Equal(t, "evicted by b-1", w.s.Fleet.Card("s1-2.w1").F(FieldTakenBack))
		assert.Equal(t, Ready, w.s.Work.Placed("s1-2").Col)
		// one eviction a blocker: the tick that follows deals it and evicts nothing more
		p, _ = TickDeal(w.s, TickReq{})
		w.must(p)
		assert.Equal(t, Working, w.s.Work.Placed("b-1").Col)
		assert.Equal(t, 1, w.s.Fleet.Count("m1", Withdrawn))
		assert.Empty(t, w.notesOf(NBlockerWaits))
	})

	t.Run("only blockers running: nothing is evicted and the blocker waits", func(t *testing.T) {
		t.Parallel()
		w := evictWorld(t)
		w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-1", "s1-2", "s1-3"}, Level: PriorityBlocker, Reason: "all blockers", Who: "coordinator"}))
		addBlocker(w, "")
		p, _ := TickDeal(w.s, TickReq{})
		w.must(p)
		assert.Equal(t, 0, w.s.Fleet.Count("m1", Withdrawn), "a blocker never evicts a blocker")
		assert.Equal(t, Ready, w.s.Work.Placed("b-1").Col)
		open := w.openOn("b-1")
		require.Len(t, open, 1, "one judgment: %v", w.s.Open)
		assert.Equal(t, NBlockerWaits, open[0].Note.Type)
		assert.Contains(t, open[0].Note.What, BlockerWaitsWhat)
		assert.Equal(t, TickDecisions[NBlockerWaits], open[0].Note.Decisions)
		// the judgment stands once while it waits
		p, _ = TickDeal(w.s, TickReq{})
		w.must(p)
		assert.Len(t, w.openOn("b-1"), 1)
		// a lane frees: the blocker is dealt and the judgment closes
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1"), As: "m1"}))
		p, _ = TickDeal(w.s, TickReq{})
		w.must(p)
		assert.Equal(t, Working, w.s.Work.Placed("b-1").Col)
		assert.Empty(t, w.openOn("b-1"), "the judgment closes when it is dealt")
	})

	t.Run("a friend's lane", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.s.Work.SetRows([]string{"s2"})
		amy := FriendSeat{Name: "amy", Width: 1, Status: Up, Tiers: []string{cardhdr.RouteFlash, cardhdr.RoutePro}}
		row := FriendRow("amy")
		w.s.Fleet.SetRows([]string{row})
		w.must(Add(w.s, AddReq{Stream: "s2", Cards: []CardAdd{{ID: "s2-1", Brief: friendBrief("only friend amy")}, {ID: "s2-2", Brief: friendBrief("only friend amy")}}}))
		p, _ := TickDeal(w.s, TickReq{Friends: []FriendSeat{amy}})
		w.must(p)
		require.Equal(t, 2, w.s.Fleet.Count(row, Ready), "her room (twice her width) is full")
		// she started s2-1 five minutes ago (its start receipt written then): her one lane runs it
		wc := w.s.Fleet.Card("s2-1.w1")
		wc.Col = Working
		wc.Fields["taken"], wc.Fields["first_taken"], wc.Fields["started"] = stamp(t0.Add(-5*time.Minute)), stamp(t0.Add(-5*time.Minute)), "1"
		w.s.Fleet.cells = nil
		amy.Running = []string{"s2-1.w1"}
		addBlocker(w, "only friend amy")
		p, _ = TickDeal(w.s, TickReq{Friends: []FriendSeat{amy}})
		w.must(p)
		wc = w.s.Fleet.Card("s2-1.w1")
		require.Equal(t, Withdrawn, wc.Col, "her running card is evicted; the ready one behind it is not running")
		assert.Equal(t, "evicted by b-1", wc.F(FieldTakenBack))
		assert.Empty(t, wc.F(FieldTakenFrom), "not taken from her: she may have it back")
		assert.Equal(t, Ready, w.s.Work.Placed("s2-1").Col)
		assert.Equal(t, Ready, w.s.Fleet.Card("s2-2.w1").Col)
		notes := w.notesOf(NEvicted)
		require.Len(t, notes, 1)
		assert.Contains(t, notes[0].What, "evicted by b-1: s2-1.w1 (normal, running 5m0s) gives its lane on "+row)
		// her lane ends by the generation: her finish at the old one is stale
		fin := Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s2-1.w1"}}, Gens: map[string]int{"s2-1.w1": 1}, As: "friend.amy"})
		require.Len(t, fin.Refused, 1)
		assert.Contains(t, fin.Refused[0].Why, "stale")
		// the tick that follows deals the blocker into her room; s2-1 waits while her lane
		// still names it (laneRunsIt) and then goes elsewhere or back to her
		p, _ = TickDeal(w.s, TickReq{Friends: []FriendSeat{amy}})
		w.must(p)
		b := w.s.Fleet.Card("b-1.w1")
		require.NotNil(t, b, "the blocker is dealt to her")
		assert.Equal(t, row, b.Row)
		assert.Equal(t, Working, w.s.Work.Placed("b-1").Col)
	})
}
