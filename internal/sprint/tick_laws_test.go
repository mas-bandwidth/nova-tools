package sprint

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
)

// The laws of the tick's accept and of its redeals, each driven through the
// tick's own parts (TickAccept, TickPresence, TickDeal, TickLevel,
// TickDeadlines): docs/SPEC-SPRINT.md sections 2 and 6, tla/DirtyTick.tla
// RedealsAreEndedTakes and RedealBoundHolds.

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
	card := func() *Card { return w.s.Fleet.Card("s1-1.w1") }
	for lap := range 2 * MaxRedeals {
		from := card().Row
		presenceWith(w, otherOf(from))
		if c := card(); c.Col != Ready || c.Row != otherOf(from) || c.Int("redeals") != 0 || c.F(FieldTakeEnded) != "" {
			t.Fatalf("lap %d, %s down with the card ready: %s:%s redeals %d take_ended %q", lap, from, c.Row, c.Col, c.Int("redeals"), c.F(FieldTakeEnded))
		}
		presenceWith(w, "m1", "m2")
	}

	from := card().Row
	takeIt(w, "s1-1.w1")
	presenceWith(w, otherOf(from))
	if c := card(); c.Col != Ready || c.Row != otherOf(from) || c.Int("redeals") != 1 || c.F("taken") != "" {
		t.Fatalf("its take ended, another member up: %s:%s redeals %d taken %q", c.Row, c.Col, c.Int("redeals"), c.F("taken"))
	}
	presenceWith(w, "m1", "m2")

	takeIt(w, "s1-1.w1")
	presenceWith(w)
	if c := card(); c.Col != Withdrawn || c.Int("redeals") != 1 || c.F(FieldTakeEnded) == "" || w.state("s1-1") != Ready {
		t.Fatalf("its take ended, no member up: %s:%s redeals %d take_ended %q, s1-1 %s", c.Row, c.Col, c.Int("redeals"), c.F(FieldTakeEnded), w.state("s1-1"))
	}
	presenceWith(w, "m1", "m2")
	tickDeal(w)
	if c := card(); c.Col != Ready || c.Int("redeals") != 2 || c.F(FieldTakeEnded) != "" || c.F("withdrawn") != "" {
		t.Fatalf("dealt again after the take that ended: %s:%s redeals %d take_ended %q", c.Row, c.Col, c.Int("redeals"), c.F(FieldTakeEnded))
	}

	presenceWith(w)
	if c := card(); c.Col != Withdrawn || c.F(FieldTakeEnded) != "" {
		t.Fatalf("withdrawn while ready: %s:%s take_ended %q", c.Row, c.Col, c.F(FieldTakeEnded))
	}
	presenceWith(w, "m1", "m2")
	tickDeal(w)
	if c := card(); c.Col != Ready || c.Int("redeals") != 2 {
		t.Fatalf("dealt again after a withdrawal while ready: %s:%s redeals %d, want its count kept at 2", c.Row, c.Col, c.Int("redeals"))
	}
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
	if c := card(); c.Col != Ready || c.Row != otherOf(from) || c.Int("redeals") != MaxRedeals {
		t.Fatalf("ready at its bound, its member down: %s:%s redeals %d, want dealt again with its count kept", c.Row, c.Col, c.Int("redeals"))
	}
	presenceWith(w, "m1", "m2")

	from = card().Row
	takeIt(w, "s1-1.w1")
	presenceWith(w, otherOf(from))
	c := card()
	if c.Col != Withdrawn || c.Int("redeals") != MaxRedeals || c.F(FieldTakeEnded) == "" || AtRedealBound(w.s, w.s.Work.Card("s1-1")) == nil {
		t.Fatalf("taken at its bound, its member down: %s:%s redeals %d take_ended %q", c.Row, c.Col, c.Int("redeals"), c.F(FieldTakeEnded))
	}
	gen := c.Int("gen")
	p := tickDeal(w)
	if card().Col != Withdrawn || card().Int("gen") != gen || len(p.Units) != 0 {
		t.Fatalf("the deal moved a card at its bound: %s:%s gen %d, %d units", card().Row, card().Col, card().Int("gen"), len(p.Units))
	}
	var bound []Note
	for _, n := range p.Notes {
		if n.Type == NBound && n.Card == "s1-1.w1" {
			bound = append(bound, n)
		}
	}
	if len(bound) != 1 || !slices.Equal(bound[0].Decisions, TickDecisions[NBound]) {
		t.Fatalf("the bound's judgment: %+v", p.Notes)
	}
}

// The level moves a ready card at a new generation and keeps its redeals and
// its untaken clock: a levelling is no redeal, and no member's arrival
// restarts the card's 15 minutes.
func TestTheTickLevelKeepsRedealsAndTheUntakenClock(t *testing.T) {
	t.Parallel()
	w := setup(t, 3)
	w.must(FleetStep(w.s, FleetReq{Op: "hold", Member: "m2"}))
	w.must(Deal(w.s, DealReq{Sel: Sel{Limit: 3}}))
	if n := w.s.Fleet.Count("m1", Ready); n != 3 {
		t.Fatalf("m1 holds %d ready cards, want all 3", n)
	}
	for _, id := range []string{"s1-1.w1", "s1-2.w1", "s1-3.w1"} {
		w.s.Fleet.Card(id).Fields["redeals"] = "2"
	}
	clock := w.s.Fleet.Card("s1-3.w1").F("untaken_since")
	w.tick(time.Minute)
	ctl := w.s.MemberCtl("m2")
	ctl.Fields["status"] = Up
	delete(ctl.Fields, "held")
	p, _ := TickLevel(w.s, TickReq{})
	w.must(p)
	c := w.s.Fleet.Card("s1-3.w1")
	if c.Row != "m2" || c.Col != Ready || c.Int("gen") != 2 {
		t.Fatalf("the level: the newest card at %s:%s gen %d, want m2:ready gen 2", c.Row, c.Col, c.Int("gen"))
	}
	if c.Int("redeals") != 2 || c.F("untaken_since") != clock || c.F("untaken_since") == "" {
		t.Fatalf("the level changed its redeals (%d) or its untaken clock (%q, was %q)", c.Int("redeals"), c.F("untaken_since"), clock)
	}
}

// The deal never cuts a second card of one attempt: a ready primary whose
// next work card exists already (written outside the sprint) is refused,
// named, and stays ready.
func TestTheTickDealRefusesAPrimaryWhoseWorkCardExists(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.s.Fleet.Put(&Card{ID: "s1-1.w1", Row: "m1", Col: Done, Score: 1, Rev: 1, Fields: map[string]string{"kind": "work", "primary": "s1-1"}})
	p, _ := TickDeal(w.s, TickReq{})
	if len(p.Units) != 0 || len(p.Refused) != 1 || p.Refused[0].Key != "s1-1" || !strings.Contains(p.Refused[0].Why, "work card s1-1.w1 exists already") {
		t.Fatalf("the deal of a primary whose work card exists: %d units, refused %+v", len(p.Units), p.Refused)
	}
	if w.state("s1-1") != Ready {
		t.Fatalf("s1-1 is %s", w.state("s1-1"))
	}
}

// A withdrawn card is dealt again to the next member round the fleet, the
// member its take ended on included: the avoid of a rework is for a new
// attempt, never for the same attempt's card dealt again.
func TestTheTickRedealIgnoresTheAvoidMember(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Deal(w.s, DealReq{Sel: Sel{Limit: 2}}))
	from := w.s.Fleet.Card("s1-1.w1").Row
	if w.s.Fleet.Card("s1-2.w1").Row == from {
		t.Fatalf("both cards dealt to %s", from)
	}
	takeIt(w, "s1-1.w1")
	presenceWith(w)
	if avoid := reworkAvoid(w.s, w.s.Work.Card("s1-1")); avoid != from {
		t.Fatalf("the avoid member of s1-1 is %q, want %s, the member its take ended on", avoid, from)
	}
	presenceWith(w, "m1", "m2")
	tickDeal(w)
	if c := w.s.Fleet.Card("s1-1.w1"); c.Row != from || c.Col != Ready || c.Int("redeals") != 1 {
		t.Fatalf("s1-1.w1 dealt again to %s:%s redeals %d, want %s, the next member round the fleet, not avoided", c.Row, c.Col, c.Int("redeals"), from)
	}
}

// Late work offers no rework: the judgment of a work card past its deadline
// offers fleet down of its member, wait and drop; a rework is a new attempt,
// and lateness is not a finding about the work.
func TestTheTickLateWorkOffersNoRework(t *testing.T) {
	t.Parallel()
	if slices.Contains(TickDecisions[NWorkLate], "rework") {
		t.Fatalf("the late work judgment's decisions: %q", TickDecisions[NWorkLate])
	}
	w := dealt(t)
	w.tick(DeadlineUnfinished + time.Minute)
	p, _ := TickDeadlines(w.s, TickReq{})
	var late []Note
	for _, n := range p.Notes {
		if n.Type == NWorkLate {
			late = append(late, n)
		}
	}
	member := w.s.Fleet.Card("s1-1.w1").Row
	if len(late) != 1 || !slices.Equal(late[0].Decisions, []string{"fleet down " + member, "wait", "drop"}) {
		t.Fatalf("the late work judgment: %+v", late)
	}
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
	if len(okReaders(w.s, w.s.Work.Card(id))) != 2 {
		w.t.Fatalf("%s is not acceptable", id)
	}
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
	if got := tickAccepts(w); !slices.Equal(got, []string{"s1-1"}) || w.state("s1-1") != Merging {
		t.Fatalf("the first accept: %v, s1-1 %s", got, w.state("s1-1"))
	}
	w.must(Return(w.s, ReturnReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "the stream branch went red"}))
	for i := range 3 {
		if got := tickAccepts(w); len(got) != 0 || w.state("s1-1") != Review {
			t.Fatalf("tick %d after the return accepted %v, s1-1 %s", i, got, w.state("s1-1"))
		}
	}
	if why := AcceptHeld(w.s.Work.Card("s1-1")); !strings.Contains(why, "returned to review at attempt 1") {
		t.Fatalf("the hold: %q", why)
	}
	var offers bool
	for _, o := range w.openOn("s1-1") {
		offers = offers || o.Note.Type == NReturned && slices.Contains(o.Note.Decisions, "accept")
	}
	if !offers {
		t.Fatalf("no open judgment on s1-1 offers accept: %+v", w.openOn("s1-1"))
	}

	// the coordinator's accept takes it
	took := reviewedOK(t)
	tickAccepts(took)
	took.must(Return(took.s, ReturnReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	took.must(Accept(took.s, AcceptReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	if took.state("s1-1") != Merging {
		t.Fatalf("the coordinator's accept of a returned primary: %s", took.state("s1-1"))
	}

	// a rework is a new attempt: its two reads are the tick's to accept
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "rebase it"}))
	takeIt(w, "s1-1.w2")
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w2"}}, Gens: gensOf(w.s, "s1-1.w2")}))
	readBothOK(w, "s1-1")
	if got := tickAccepts(w); !slices.Equal(got, []string{"s1-1"}) || w.state("s1-1") != Merging {
		t.Fatalf("attempt 2 with its own reads: accepted %v, s1-1 %s", got, w.state("s1-1"))
	}
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
		if got := tickAccepts(w); len(got) != 0 || w.state("s1-1") != Review {
			t.Fatalf("tick %d with its CI red accepted %v, s1-1 %s", i, got, w.state("s1-1"))
		}
	}
	w.must(RecordCI(w.s, CIReq{Sel: Sel{IDs: []string{"s1-1"}}, Run: "2"}))
	if got := tickAccepts(w); !slices.Equal(got, []string{"s1-1"}) {
		t.Fatalf("green at its head: accepted %v", got)
	}

	old := reviewedOK(t)
	old.must(RecordCI(old.s, CIReq{Sel: Sel{IDs: []string{"s1-1"}}, Red: true, Run: "1", Head: "an-older-head"}))
	if got := tickAccepts(old); !slices.Equal(got, []string{"s1-1"}) {
		t.Fatalf("red for an older head: accepted %v", got)
	}
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
		if red {
			var ci []Open
			for _, o := range w.openOn("s1-1") {
				if o.Note.Type == NCIRed {
					ci = append(ci, o)
				}
			}
			if len(ci) != 1 {
				t.Fatalf("the CI judgment: %+v", w.openOn("s1-1"))
			}
			w.must(Ack(w.s, AckReq{Notes: []string{ci[0].Note.ID}, Reason: "the red is a flaky runner", Who: "coordinator"}))
		}
		if n := len(w.notesOf(NReadyToAccept)); n != map[bool]int{false: 0, true: 1}[red] {
			t.Fatalf("CI red %v on a running machine: %d ready to accept judgments", red, n)
		}
	}
}
