package sprint

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// evictWorld is one member m1 at width 3, at its room: s1-1, s1-2 and s1-3 working (taken
// 10, 2 and 20 minutes ago), s1-4, s1-5 and s1-6 ready behind them, never taken. With
// behind false the three ready cards are not admitted: the lanes are held and the row has
// room, so a blocker is dealt into its ready queue.
func evictWorld(t *testing.T, behind bool) *world {
	t.Helper()
	w := newWorld(t, "reader-a", "reader-b")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1", Width: 3}))
	n := 3
	if behind {
		n = 6
	}
	w.must(Add(w.s, AddReq{Brief: "a card", Stream: "s1", Count: n}))
	w.must(Deal(w.s, DealReq{Sel: Sel{Limit: n}}))
	require.Equal(t, n, w.s.Fleet.Count("m1", Ready))
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

// readOn puts a read card of the primary pr working on the row.
func readOn(w *world, row, pr string) *Card {
	rc := &Card{ID: ReadCardID(pr, 1, row), Row: row, Col: Working, Score: 1, Rev: 1, Fields: map[string]string{
		"kind": "read", "primary": pr, "stream": "r", "reader": row, "attempt": "1", "head": "h-" + pr, FieldReadCard: "1", "taken": stamp(t0)}}
	w.s.Fleet.Put(rc)
	return rc
}

// A blocker is stored, dealt first, and evicts (docs/SPEC-SPRINT.md section 1, "Priority";
// tla/Priority.tla; the owner, 2026-10-06): on a row at its room, a ready blocker evicts the
// lowest level first, then among equals the one running the shortest, never a blocker, and
// is dealt into the room it frees in the same plan; the evicted card goes back to ready at
// its level, its lane ends by the generation (a finish at the old one is stale), the record
// says "evicted by <blocker>", and the member's take takes the blocker first. A blocker dealt
// and ready behind held lanes evicts for its lane too. With only blockers, or reads, running
// nothing is evicted and the blocker waits under one judgment.
func TestABlockerIsStoredAndEvictsTheLowestThenTheShortest(t *testing.T) {
	t.Parallel()

	t.Run("the low card first, and the blocker into the room in the same plan", func(t *testing.T) {
		t.Parallel()
		w := evictWorld(t, true)
		w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-1"}, Level: PriorityLow, Reason: "background", Who: "coordinator"}))
		addBlocker(w, "")
		p, _ := TickDeal(w.s, TickReq{})
		w.must(p)
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
		// the blocker is dealt into the room the eviction frees, in the same plan
		b := w.s.Fleet.Card("b-1.w1")
		require.NotNil(t, b, "the blocker is dealt in the eviction's plan")
		assert.Equal(t, [2]string{"m1", Ready}, [2]string{b.Row, b.Col})
		assert.Equal(t, PriorityBlocker, QueuePriority(b), "the work card carries the level")
		assert.Equal(t, Working, w.s.Work.Placed("b-1").Col)
		// the member's lane: a finish at the old generation is stale (the member reaps it)
		fin := Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: map[string]int{"s1-1.w1": 1}, As: "m1"})
		require.Len(t, fin.Refused, 1)
		assert.Contains(t, fin.Refused[0].Why, "stale")
		// and the member's take takes the blocker before the three ready normals dealt earlier
		w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{Limit: 1}}))
		assert.Equal(t, Working, w.s.Fleet.Card("b-1.w1").Col, "the blocker is taken first")
		assert.Equal(t, Ready, w.s.Fleet.Card("s1-4.w1").Col)
		// the evicted card's next generation starts from the branch that holds its pushed work
		pk := PacketOf("", 0, wc, pr, nil, nil)
		assert.Equal(t, BranchOf("", 0, "s1-1.w1", 1), pk.Base, "the carried generation's branch is the base")
		assert.Empty(t, pk.BaseHead, "the tick read no pushed tip")
	})

	t.Run("with no low card, the shortest running", func(t *testing.T) {
		t.Parallel()
		w := evictWorld(t, true)
		addBlocker(w, "")
		p, _ := TickDeal(w.s, TickReq{})
		w.must(p)
		assert.Equal(t, Withdrawn, w.s.Fleet.Card("s1-2.w1").Col, "the normal running 2 minutes, not the ones at 10 and 20")
		assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)
		assert.Equal(t, Working, w.s.Fleet.Card("s1-3.w1").Col)
		assert.Equal(t, "evicted by b-1", w.s.Fleet.Card("s1-2.w1").F(FieldTakenBack))
		assert.Equal(t, Ready, w.s.Work.Placed("s1-2").Col)
		assert.Equal(t, Working, w.s.Work.Placed("b-1").Col, "dealt in the same plan")
		// one eviction a blocker: the tick that follows evicts nothing more
		p, _ = TickDeal(w.s, TickReq{})
		w.must(p)
		assert.Equal(t, 1, w.s.Fleet.Count("m1", Withdrawn))
		assert.Empty(t, w.notesOf(NBlockerWaits))
	})

	t.Run("only blockers running: nothing is evicted and the blocker waits", func(t *testing.T) {
		t.Parallel()
		w := evictWorld(t, true)
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

	t.Run("a blocker dealt and ready behind held lanes evicts for its lane", func(t *testing.T) {
		t.Parallel()
		w := evictWorld(t, false) // three lanes held, room for three more in ready
		w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-1"}, Level: PriorityLow, Reason: "background", Who: "coordinator"}))
		addBlocker(w, "")
		p, _ := TickDeal(w.s, TickReq{})
		w.must(p)
		b := w.s.Fleet.Card("b-1.w1")
		require.NotNil(t, b)
		assert.Equal(t, [2]string{"m1", Ready}, [2]string{b.Row, b.Col}, "the row has room: the blocker is dealt into its ready queue")
		assert.Equal(t, 0, w.s.Fleet.Count("m1", Withdrawn), "nothing is evicted by the deal that places it")
		// the tick after: its lanes are all held, so one is evicted for it
		p, _ = TickDeal(w.s, TickReq{})
		w.must(p)
		assert.Equal(t, Withdrawn, w.s.Fleet.Card("s1-1.w1").Col, "the low card is evicted for the blocker queued ready")
		assert.Equal(t, "evicted by b-1", w.s.Fleet.Card("s1-1.w1").F(FieldTakenBack))
		assert.Empty(t, w.openOn("b-1"))
		// the lane that frees is the blocker's: the member's take takes it first
		w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{Limit: 1}}))
		assert.Equal(t, Working, w.s.Fleet.Card("b-1.w1").Col)
		// and nothing more is evicted once it runs (the evicted card is dealt again into the room)
		p, _ = TickDeal(w.s, TickReq{})
		w.must(p)
		assert.Len(t, w.notesOf(NEvicted), 1)
		assert.Equal(t, Ready, w.s.Fleet.Card("s1-1.w1").Col, "dealt again at its level, behind the lanes")
	})

	t.Run("reads are half slots, and lanes of reads are a judgment", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a", "reader-b")
		w.s.Work.SetProp(PropReadCards, ReadCardsOnWord)
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1", Width: 1}))
		w.s.Work.SetRows([]string{"r"})
		for _, id := range []string{"r-1", "r-2"} {
			w.s.Work.Put(&Card{ID: id, Row: "r", Col: Review, Score: 1, Rev: 1, Fields: map[string]string{"kind": "primary", "attempt": "1", "stream": "r", "brief": id + ": in review", "head": "h-" + id}})
			readOn(w, "m1", id)
		}
		addBlocker(w, "")
		p, _ := TickDeal(w.s, TickReq{})
		w.must(p)
		b := w.s.Fleet.Card("b-1.w1")
		require.NotNil(t, b, "two reads hold one slot of the width: the row has room for the blocker")
		assert.Equal(t, Ready, b.Col)
		assert.Equal(t, 0, w.s.Fleet.Count("m1", Withdrawn), "a read is never evicted")
		// its lanes hold reads alone: nothing to evict, and the blocker waits under a judgment
		p, _ = TickDeal(w.s, TickReq{})
		w.must(p)
		assert.Equal(t, 0, w.s.Fleet.Count("m1", Withdrawn))
		open := w.openOn("b-1")
		require.Len(t, open, 1, "%v", w.s.Open)
		assert.Equal(t, NBlockerWaits, open[0].Note.Type)
		assert.Contains(t, open[0].Note.What, "every lane holds a blocker or a read")
		assert.Contains(t, open[0].Note.What, "2 hold reads")
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
		// the blocker takes the lane that frees, in the same plan, as her finish's next does
		b := w.s.Fleet.Card("b-1.w1")
		require.NotNil(t, b, "the blocker is dealt to her in the eviction's plan")
		assert.Equal(t, [2]string{row, Working}, [2]string{b.Row, b.Col})
		assert.NotEmpty(t, b.F("taken"))
		assert.Equal(t, Working, w.s.Work.Placed("b-1").Col)
		// her lane ends by the generation: her finish at the old one is stale
		fin := Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s2-1.w1"}}, Gens: map[string]int{"s2-1.w1": 1}, As: "friend.amy"})
		require.Len(t, fin.Refused, 1)
		assert.Contains(t, fin.Refused[0].Why, "stale")
		// the evicted card's next generation starts from the branch that holds her pushed work
		assert.Equal(t, BranchOf("", 0, "s2-1.w1", 1), PacketOf("", 0, wc, w.s.Work.Placed("s2-1"), nil, nil).Base)
	})
}

// The verb priority raises a dealt card in place: its work card, ready or working, and its live
// read cards take the level, so a member's take and a queue rank them by the level set and the
// eviction sees a blocker queued ready (the cold read of nova-tools#5405, item 2).
func TestPriorityRaisesADealtCardInPlace(t *testing.T) {
	t.Parallel()
	w := evictWorld(t, false)
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-2"}, Level: PriorityBlocker, Reason: "now", Who: "coordinator"}))
	assert.Equal(t, PriorityBlocker, w.s.Fleet.Card("s1-2.w1").F(FieldPriority), "the working card takes the level")
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-2"}, Level: PriorityNormal, Reason: "back", Who: "coordinator"}))
	assert.Empty(t, w.s.Fleet.Card("s1-2.w1").F(FieldPriority), "normal is no level on the card")
	// a read card of a primary in review takes the read's inherited level
	w.s.Work.SetRows([]string{"s1", "r"})
	w.s.Work.Put(&Card{ID: "r-1", Row: "r", Col: Review, Score: 1, Rev: 1, Fields: map[string]string{"kind": "primary", "attempt": "1", "stream": "r", "brief": "r-1: in review", "head": "h"}})
	rc := readOn(w, "m1", "r-1")
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"r-1"}, Level: PriorityCritical, Reason: "now", Who: "coordinator"}))
	assert.Equal(t, PriorityCritical, w.s.Fleet.Card(rc.ID).F(FieldPriority), "the read inherits critical")
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"r-1"}, Level: PriorityLow, Reason: "later", Who: "coordinator"}))
	assert.Empty(t, w.s.Fleet.Card(rc.ID).F(FieldPriority), "a low primary's read is reader: no level on the card")
	assert.Contains(t, w.notesOf(NPrioritySet)[0].What, "1 dealt cards re-levelled")
}

// A member's take ranks its ready cards by the level each carries: a blocker primary's read
// (its inherited level) before critical work, the reader-level reads before normal work (item
// 6 of the cold read: ladderOrder took every read as reader).
func TestATakeRanksByTheCarriedLevel(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a", "reader-b")
	w.s.Work.SetProp(PropReadCards, ReadCardsOnWord)
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1", Width: 1}))
	w.must(Add(w.s, AddReq{Brief: "c: critical work\nPRIORITY: critical\n\nThe task.", Stream: "s1", Count: 1}))
	w.must(Deal(w.s, DealReq{Sel: Sel{Limit: 1}}))
	w.s.Work.SetRows([]string{"s1", "r"})
	w.s.Work.Put(&Card{ID: "r-1", Row: "r", Col: Review, Score: 1, Rev: 1, Fields: map[string]string{"kind": "primary", "attempt": "1", "stream": "r", "brief": "r-1: in review", "head": "h", FieldPriority: PriorityBlocker}})
	rc := readOn(w, "m1", "r-1")
	rc.Col, rc.Fields[FieldPriority] = Ready, PriorityBlocker
	w.s.Fleet.cells = nil
	assert.Equal(t, []string{rc.ID, "s1-1.w1"}, ids(queueOrder([]*Card{w.s.Fleet.Card("s1-1.w1"), rc})), "the blocker's read before the critical work")
	assert.Equal(t, []string{"s1-1.w1", rc.ID}, ids(ladderOrder([]*Card{w.s.Fleet.Card("s1-1.w1"), rc})), "ladderOrder took the read as reader")
	w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{Limit: 1}}))
	assert.Equal(t, Working, w.s.Fleet.Card(rc.ID).Col, "the take takes the blocker's read first")
	assert.Equal(t, Ready, w.s.Fleet.Card("s1-1.w1").Col)
}

func ids(cards []*Card) []string {
	var out []string
	for _, c := range cards {
		out = append(out, c.ID)
	}
	return out
}

// The carry (friend_take.go, priority_evict.go): a work card at a later generation that
// carries the generation whose branch holds its pushed work starts from that branch, and from
// the pushed head when the take-back read one (the hold's carry_head).
func TestANextGenerationStartsFromTheCarriedBranchAndHead(t *testing.T) {
	t.Parallel()
	primary := &Card{ID: "s1-1", Row: "s1", Col: Working, Fields: map[string]string{"kind": "primary", "attempt": "1", "brief": "s1-1: a card"}}
	plain := &Card{ID: "s1-1.w1", Row: "m1", Col: Ready, Fields: map[string]string{"kind": "work", "primary": "s1-1", "attempt": "1", "gen": "2"}}
	assert.Empty(t, PacketOf("", 3, plain, primary, nil, nil).Base, "no carry: the card's base")
	carried := &Card{ID: "s1-1.w1", Row: "m1", Col: Ready, Fields: map[string]string{"kind": "work", "primary": "s1-1", "attempt": "1", "gen": "2", FieldCarryGen: "1"}}
	p := PacketOf("", 3, carried, primary, nil, nil)
	assert.Equal(t, BranchOf("", 3, "s1-1.w1", 1), p.Base, "the carried generation's branch")
	assert.Empty(t, p.BaseHead)
	const tip = "0123456789abcdef0123456789abcdef01234567"
	carried.Fields[FieldCarryHead] = tip
	p = PacketOf("", 3, carried, primary, nil, nil)
	assert.Equal(t, tip, p.BaseHead, "the pushed head the hold read")
	assert.Equal(t, 1, p.BaseAttempt, "of this attempt")
	assert.Contains(t, NextLine(nil), "no attempt pushed a head")
}
