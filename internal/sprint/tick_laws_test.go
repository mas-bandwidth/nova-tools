package sprint

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
)

// The laws of the tick's accept and of its redeals, each driven through the
// tick's own parts (TickAccept, TickPresence, TickDeal, TickLevel,
// TickDeadlines): docs/SPEC-SPRINT.md sections 2 and 6, tla/DirtyTick.tla
// AcceptHolds, RedealsAreEndedTakes and RedealBoundHolds.

// beating is a tick given a fresh beat of each member named, and of no
// other: a member up that is not named goes down at the presence part.
func beating(w *world, members ...string) TickReq {
	beats := map[string]Beat{}
	for _, m := range members {
		beats[m] = NextBeat(Beat{}, w.s.Now, 0, hostload.HowCPU, hostload.State{})
	}
	return TickReq{Beats: beats}
}

// presenceWith applies the tick's presence part with these members beating.
func presenceWith(w *world, members ...string) {
	w.t.Helper()
	p, _ := TickPresence(w.s, beating(w, members...))
	w.must(p)
}

// tickDeal applies the tick's deal part.
func tickDeal(w *world) Plan {
	w.t.Helper()
	p, _ := TickDeal(w.s, TickReq{})
	return w.must(p)
}

// otherOf is the other of the two members.
func otherOf(m string) string {
	if m == "m1" {
		return "m2"
	}
	return "m1"
}

// takeIt is the worker of the member holding the work card taking it.
func takeIt(w *world, id string) {
	w.t.Helper()
	wc := w.s.Fleet.Card(id)
	w.must(Take(w.s, TakeReq{As: wc.Row, Sel: Sel{IDs: []string{id}}, Gens: gensOf(w.s, id)}))
}

// cardAt is a work card's place and the fields of its redeals, as one value
// a test compares whole.
type cardAt struct {
	Row, Col string
	Redeals  int
	Ended    bool
}

func placeOfCard(c *Card) cardAt {
	return cardAt{Row: c.Row, Col: c.Col, Redeals: c.Int("redeals"), Ended: c.F(FieldTakeEnded) != ""}
}

// A card is redealt for repeated failure at members, never for members
// flapping while it sat ready: the holder of a ready card going down deals it
// again with its count kept, however often; a take that ends without a finish
// counts at once when another member takes the card, and, withdrawn because
// no member is up, counts at the deal that places it again; a card withdrawn
// while ready keeps its count there too.
func TestTheTickCountsARedealOnlyForATakeThatEnded(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	card := func() cardAt { return placeOfCard(w.s.Fleet.Card("s1-1.w1")) }
	for lap := range 2 * MaxRedeals {
		from := card().Row
		presenceWith(w, otherOf(from))
		assert.Equal(t, cardAt{otherOf(from), Ready, 0, false}, card(), "lap %d, %s down with the card ready", lap, from)
		presenceWith(w, "m1", "m2")
	}

	from := card().Row
	takeIt(w, "s1-1.w1")
	presenceWith(w, otherOf(from))
	assert.Equal(t, cardAt{otherOf(from), Ready, 1, false}, card(), "its take ended, another member up")
	presenceWith(w, "m1", "m2")

	takeIt(w, "s1-1.w1")
	presenceWith(w)
	c := card()
	assert.Equal(t, Withdrawn, c.Col, "its take ended, no member up")
	assert.Equal(t, 1, c.Redeals, "its take ended, no member up: counted when it is dealt again")
	assert.True(t, c.Ended, "its take ended, no member up: marked")
	assert.Equal(t, Ready, w.state("s1-1"))
	presenceWith(w, "m1", "m2")
	tickDeal(w)
	c = card()
	assert.Equal(t, Ready, c.Col, "dealt again after the take that ended")
	assert.Equal(t, 2, c.Redeals, "dealt again after the take that ended")
	assert.False(t, c.Ended, "dealt again after the take that ended")

	presenceWith(w)
	c = card()
	assert.Equal(t, Withdrawn, c.Col, "withdrawn while ready")
	assert.False(t, c.Ended, "withdrawn while ready")
	presenceWith(w, "m1", "m2")
	tickDeal(w)
	c = card()
	assert.Equal(t, Ready, c.Col, "dealt again after a withdrawal while ready")
	assert.Equal(t, 2, c.Redeals, "dealt again after a withdrawal while ready: its count kept")
	w.clean("after the flapping")
}

// At its bound a card is retired only when a take of it ends: ready at the
// bound, its member going down deals it again; taken at the bound, its member
// going down withdraws it with a member still up, and the deal leaves it
// there and names it in the bound's judgment.
func TestTheTickRetiresACardOnlyWhenATakeEndsAtItsBound(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	card := func() *Card { return w.s.Fleet.Card("s1-1.w1") }
	card().Fields["redeals"] = itoa(MaxRedeals)

	from := card().Row
	presenceWith(w, otherOf(from))
	assert.Equal(t, cardAt{otherOf(from), Ready, MaxRedeals, false}, placeOfCard(card()), "ready at its bound, its member down: dealt again with its count kept")
	presenceWith(w, "m1", "m2")

	from = card().Row
	takeIt(w, "s1-1.w1")
	presenceWith(w, otherOf(from))
	c := placeOfCard(card())
	assert.Equal(t, Withdrawn, c.Col, "taken at its bound, its member down")
	assert.Equal(t, MaxRedeals, c.Redeals, "taken at its bound, its member down")
	assert.True(t, c.Ended, "taken at its bound, its member down")
	require.NotNil(t, AtRedealBound(w.s, w.s.Work.Card("s1-1")), "at its bound")
	gen := card().Int("gen")
	p := tickDeal(w)
	assert.Empty(t, p.Units, "the deal moved a card at its bound")
	assert.Equal(t, Withdrawn, card().Col)
	assert.Equal(t, gen, card().Int("gen"))
	var bound []Note
	for _, n := range p.Notes {
		if n.Type == NBound && n.Card == "s1-1.w1" {
			bound = append(bound, n)
		}
	}
	require.Len(t, bound, 1, "the bound's judgment: %+v", p.Notes)
	assert.Equal(t, TickDecisions[NBound], bound[0].Decisions)
}

// The level moves a ready card at a new generation and keeps its redeals and
// its untaken clock: a levelling is no redeal, and no member's arrival
// restarts the card's 15 minutes.
func TestTheTickLevelKeepsRedealsAndTheUntakenClock(t *testing.T) {
	t.Parallel()
	w := setup(t, 3)
	w.must(FleetStep(w.s, FleetReq{Op: "hold", Member: "m2"}))
	w.must(Deal(w.s, DealReq{Sel: Sel{Limit: 3}}))
	require.Equal(t, 3, w.s.Fleet.Count("m1", Ready), "m1 holds every ready card")
	for _, id := range []string{"s1-1.w1", "s1-2.w1", "s1-3.w1"} {
		w.s.Fleet.Card(id).Fields["redeals"] = "2"
	}
	clock := w.s.Fleet.Card("s1-3.w1").F("untaken_since")
	require.NotEmpty(t, clock)
	w.tick(time.Minute)
	ctl := w.s.MemberCtl("m2")
	ctl.Fields["status"] = Up
	delete(ctl.Fields, "held")
	p, _ := TickLevel(w.s, TickReq{})
	w.must(p)
	c := w.s.Fleet.Card("s1-3.w1")
	assert.Equal(t, cardAt{"m2", Ready, 2, false}, placeOfCard(c), "the newest card levelled, its redeals kept")
	assert.Equal(t, 2, c.Int("gen"))
	assert.Equal(t, clock, c.F("untaken_since"), "the level restarted its untaken clock")
}

// The deal never cuts a second card of one attempt: a ready primary whose
// next work card exists already (written outside the sprint) is refused,
// named, and stays ready.
func TestTheTickDealRefusesAPrimaryWhoseWorkCardExists(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.s.Fleet.Put(&Card{ID: "s1-1.w1", Row: "m1", Col: Done, Score: 1, Rev: 1, Fields: map[string]string{"kind": "work", "primary": "s1-1"}})
	p, _ := TickDeal(w.s, TickReq{})
	assert.Empty(t, p.Units)
	require.Len(t, p.Refused, 1)
	assert.Equal(t, "s1-1", p.Refused[0].Key)
	assert.Contains(t, p.Refused[0].Why, "work card s1-1.w1 exists already")
	assert.Equal(t, Ready, w.state("s1-1"))
}

// A withdrawn card is dealt again to the next member round the fleet, the
// member its take ended on included: the avoid of a rework is for a new
// attempt, never for the same attempt's card dealt again.
func TestTheTickRedealIgnoresTheAvoidMember(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Deal(w.s, DealReq{Sel: Sel{Limit: 2}}))
	from := w.s.Fleet.Card("s1-1.w1").Row
	require.NotEqual(t, from, w.s.Fleet.Card("s1-2.w1").Row, "both cards dealt to one member")
	takeIt(w, "s1-1.w1")
	presenceWith(w)
	require.Equal(t, from, reworkAvoid(w.s, w.s.Work.Card("s1-1")), "the avoid member is the member its take ended on")
	presenceWith(w, "m1", "m2")
	tickDeal(w)
	assert.Equal(t, cardAt{from, Ready, 1, false}, placeOfCard(w.s.Fleet.Card("s1-1.w1")), "dealt again to the next member round the fleet, not avoided")
}

// Late work offers no rework: the judgment of a work card past its deadline
// offers fleet down of its member, wait and drop; a rework is a new attempt,
// and lateness is not a finding about the work.
func TestTheTickLateWorkOffersNoRework(t *testing.T) {
	t.Parallel()
	assert.NotContains(t, TickDecisions[NWorkLate], "rework")
	w := dealt(t)
	w.tick(DeadlineUnfinished + time.Minute)
	p, _ := TickDeadlines(w.s, TickReq{})
	var late []Note
	for _, n := range p.Notes {
		if n.Type == NWorkLate {
			late = append(late, n)
		}
	}
	require.Len(t, late, 1)
	member := w.s.Fleet.Card("s1-1.w1").Row
	assert.Equal(t, []string{"fleet down " + member, "wait", "drop"}, late[0].Decisions)
}

// reviewedOK is setup with s1-1 worked, asked, and read ok by both its
// readers: in review, acceptable.
func reviewedOK(t *testing.T) *world {
	t.Helper()
	w := dealt(t)
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1")}))
	readBothOK(w, "s1-1")
	return w
}

// readBothOK asks the primary's readers, unless its finish asked them
// already (a rework's attempt), and reports both ok.
func readBothOK(w *world, id string) {
	w.t.Helper()
	pr := w.s.Work.Card(id)
	if len(readsAt(w.s, pr, pr.Int("attempt"))) == 0 {
		w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{id}}}))
	}
	for _, rc := range readsAt(w.s, pr, pr.Int("attempt")) {
		w.must(Read(w.s, ReadReq{As: rc.F("reader"), Verdict: "ok", Sel: Sel{IDs: []string{rc.ID}}}))
	}
	require.Len(w.t, okReaders(w.s, w.s.Work.Card(id)), 2, "%s is acceptable", id)
}

// tickAccepts applies the tick's accept part and says what it accepted.
func tickAccepts(w *world) []string {
	w.t.Helper()
	p, _ := TickAccept(w.s, TickReq{})
	w.must(p)
	var out []string
	for _, u := range p.Units {
		out = append(out, u.Key)
	}
	return out
}

// A primary the coordinator returned to review is not accepted again by the
// tick on the reads that stand: the returned judgment, which offers accept,
// is the coordinator's. A new attempt, with its own two reads, is the tick's
// again; and the coordinator's own accept takes a returned primary.
func TestTheTickDoesNotAcceptAReturnedPrimaryOnItsStandingReads(t *testing.T) {
	t.Parallel()
	w := reviewedOK(t)
	require.Equal(t, []string{"s1-1"}, tickAccepts(w), "the first accept")
	w.must(Return(w.s, ReturnReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "the stream branch went red"}))
	for i := range 3 {
		assert.Empty(t, tickAccepts(w), "tick %d after the return", i)
		assert.Equal(t, Review, w.state("s1-1"), "tick %d after the return", i)
	}
	assert.Contains(t, AcceptHeld(w.s.Work.Card("s1-1")), "returned to review at attempt 1")
	var offers bool
	for _, o := range w.openOn("s1-1") {
		offers = offers || o.Note.Type == NReturned && slices.Contains(o.Note.Decisions, "accept")
	}
	assert.True(t, offers, "an open judgment on s1-1 offers accept: %+v", w.openOn("s1-1"))

	// the coordinator's accept takes it
	took := reviewedOK(t)
	tickAccepts(took)
	took.must(Return(took.s, ReturnReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	took.must(Accept(took.s, AcceptReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	assert.Equal(t, Merging, took.state("s1-1"), "the coordinator's accept of a returned primary")

	// a rework is a new attempt: its two reads are the tick's to accept
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "rebase it"}))
	takeIt(w, "s1-1.w2")
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w2"}}, Gens: gensOf(w.s, "s1-1.w2")}))
	readBothOK(w, "s1-1")
	assert.Equal(t, []string{"s1-1"}, tickAccepts(w), "attempt 2 with its own reads")
	assert.Equal(t, Merging, w.state("s1-1"))
	w.clean("accepted at attempt 2")
}

// A primary whose CI is red at its head is not accepted by the tick: the red
// is the coordinator's judgment. Green at its head lets the tick accept it;
// a red for another head does not hold it.
func TestTheTickDoesNotAcceptAPrimaryWhoseCIIsRedAtItsHead(t *testing.T) {
	t.Parallel()
	w := reviewedOK(t)
	w.must(RecordCI(w.s, CIReq{Sel: Sel{IDs: []string{"s1-1"}}, Red: true, Run: "1"}))
	for i := range 3 {
		assert.Empty(t, tickAccepts(w), "tick %d with its CI red", i)
		assert.Equal(t, Review, w.state("s1-1"), "tick %d with its CI red", i)
	}
	w.must(RecordCI(w.s, CIReq{Sel: Sel{IDs: []string{"s1-1"}}, Run: "2"}))
	assert.Equal(t, []string{"s1-1"}, tickAccepts(w), "green at its head")

	old := reviewedOK(t)
	old.must(RecordCI(old.s, CIReq{Sel: Sel{IDs: []string{"s1-1"}}, Red: true, Run: "1", Head: "an-older-head"}))
	assert.Equal(t, []string{"s1-1"}, tickAccepts(old), "red for an older head")
}

// A primary in review is never silent: on a RUNNING machine an acceptable
// primary the tick holds (its CI red at its head) is told as ready to accept
// once its CI judgment is acknowledged; one the tick accepts is not.
func TestAnAcceptablePrimaryTheTickHoldsIsReadyToAccept(t *testing.T) {
	t.Parallel()
	for _, red := range []bool{false, true} {
		w := dealt(t)
		w.s.Running = true
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1")}))
		if red {
			w.must(RecordCI(w.s, CIReq{Sel: Sel{IDs: []string{"s1-1"}}, Red: true, Run: "1"}))
		}
		readBothOK(w, "s1-1")
		want := 0
		if red {
			var ci []Open
			for _, o := range w.openOn("s1-1") {
				if o.Note.Type == NCIRed {
					ci = append(ci, o)
				}
			}
			require.Len(t, ci, 1, "the CI judgment")
			w.must(Ack(w.s, AckReq{Notes: []string{ci[0].Note.ID}, Reason: "the red is a flaky runner", Who: "coordinator"}))
			want = 1
		}
		assert.Len(t, w.notesOf(NReadyToAccept), want, "CI red %v on a running machine: ready to accept judgments", red)
	}
}
