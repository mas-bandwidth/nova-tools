package sprint

import (
	"strings"
	"testing"
	"time"
)

// The no-stall rule, one state per holder: each holder holding, and each
// failing, built by hand on the core's own world.

func running(w *world) HeldState { return HeldState{Snap: w.s, Running: true} }

func stopped(w *world) HeldState { return HeldState{Snap: w.s} }

func mustHold(t *testing.T, h HeldState, id, by string) Hold {
	t.Helper()
	hd := Holder(h, h.Snap.Now, id)
	if hd.By != by {
		t.Fatalf("%s is held by %q, want %q: %s", id, hd.By, by, hd)
	}
	for _, f := range Unheld(h, h.Snap.Now) {
		if f.Subject == id {
			t.Fatalf("%s is held (%s) and Unheld names it: %s", id, hd, f)
		}
	}
	return hd
}

func mustStall(t *testing.T, h HeldState, id, why string) Finding {
	t.Helper()
	hd := Holder(h, h.Snap.Now, id)
	if !hd.Stalled() || !strings.Contains(hd.Why, why) {
		t.Fatalf("%s: %s, want stalled: ...%s...", id, hd, why)
	}
	for _, f := range Unheld(h, h.Snap.Now) {
		if f.Subject == id {
			return f
		}
	}
	t.Fatalf("%s is stalled (%s) and Unheld does not name it", id, hd)
	return Finding{}
}

// dealt is setup with s1-1 dealt to a member and taken.
func dealt(t *testing.T) *world {
	w := setup(t, 1)
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	wc := w.s.Fleet.Card("s1-1.w1")
	w.must(Take(w.s, TakeReq{As: wc.Row, Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
	return w
}

// inReviewFailed is setup with s1-1's work come back failed: in review, its
// failed judgment open.
func inReviewFailed(t *testing.T) *world {
	w := dealt(t)
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: "red"}))
	return w
}

func TestHeldByAnOutsideActorBeforeItsDeadline(t *testing.T) {
	t.Parallel()
	w := dealt(t)
	hd := mustHold(t, running(w), "s1-1", HeldByActor)
	if !strings.Contains(hd.Why, "s1-1.w1@1") {
		t.Fatalf("the holder does not name the card and its generation: %s", hd)
	}
	// Past the deadline the actor no longer holds it: the next tick writes the
	// late judgment, and once written the judgment holds it.
	w.tick(DeadlineUnfinished + time.Minute)
	hd = mustHold(t, running(w), "s1-1", HeldByTick)
	if !strings.Contains(hd.Why, NWorkLate) {
		t.Fatalf("past its deadline: %s", hd)
	}
	p, _ := TickDeadlines(w.s, TickReq{})
	w.must(p)
	mustHold(t, running(w), "s1-1", HeldByJudgment)
}

func TestNotHeldByAnActorOfAMemberThatIsDown(t *testing.T) {
	t.Parallel()
	w := dealt(t)
	// The member is marked down by hand, its card left where it was: no tick
	// part moves it and nothing is open on it.
	wc := w.s.Fleet.Card("s1-1.w1")
	w.s.MemberCtl(wc.Row).Fields["status"] = Down
	f := mustStall(t, running(w), "s1-1", "no live work card of an up member")
	if f.Root != "" || !contains(f.Decisions, "fleet down "+wc.Row) || !contains(f.Decisions, "drop") {
		t.Fatalf("the finding: %+v", f)
	}
}

func TestHeldByTheNextTick(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	hd := mustHold(t, running(w), "s1-1", HeldByTick)
	if !strings.Contains(hd.Why, "working card=s1-1.w1") {
		t.Fatalf("the next tick deals it: %s", hd)
	}
}

func TestNotHeldWhenTheTickCannotDealIt(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	// A card of the next attempt exists already (a writer outside the
	// sprint): deal refuses it, and with room in the queues it waits for
	// nothing.
	w.s.Fleet.Put(&Card{ID: "s1-1.w1", Row: "m1", Col: Done, Score: 1, Rev: 1, Fields: map[string]string{"kind": "work", "primary": "s1-1"}})
	mustStall(t, running(w), "s1-1", "a member has room in its ready queue, and nothing deals it")
}

func TestHeldByAnOpenJudgment(t *testing.T) {
	t.Parallel()
	w := inReviewFailed(t)
	hd := mustHold(t, running(w), "s1-1", HeldByJudgment)
	if !strings.Contains(hd.Why, NWorkFailed) {
		t.Fatalf("held by the failed judgment: %s", hd)
	}
}

func TestNotHeldWhenTheJudgmentIsGone(t *testing.T) {
	t.Parallel()
	w := inReviewFailed(t)
	w.s.Open = nil // closed without the step that writes what it needs next
	f := mustStall(t, running(w), "s1-1", "its work came back failed, and no judgment is open on it")
	if !contains(f.Decisions, "rework") || contains(f.Decisions, "ask") {
		t.Fatalf("a failed primary's decisions: %v", f.Decisions)
	}
	// A stall judgment open on it does not hold it: the rule would close its
	// own interrupt.
	w.note(Note{Kind: Judgment, Type: NStalled, Stream: "s1", Primaries: []string{"s1-1"}, Count: 1, At: w.s.Now})
	mustStall(t, running(w), "s1-1", "no judgment is open on it")
}

func TestHeldByWhatItWaitsOn(t *testing.T) {
	t.Parallel()
	w := dealt(t)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"late"}, Needs: []string{"s1-1"}}))
	hd := mustHold(t, running(w), "late", HeldByWaiting)
	if !strings.Contains(hd.Why, "needs s1-1 (a)") {
		t.Fatalf("the chain: %s", hd)
	}
	// Behind a reached sentinel whose judgment is open.
	w2 := setup(t, 0)
	w2.must(Add(w2.s, AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true}))
	w2.must(Add(w2.s, AddReq{Stream: "s1", IDs: []string{"after"}}))
	mustHold(t, running(w2), "stop", HeldByJudgment)
	hd = mustHold(t, running(w2), "after", HeldByWaiting)
	if !strings.Contains(hd.Why, "waits behind sentinel stop (c)") {
		t.Fatalf("behind the sentinel: %s", hd)
	}
}

func TestNotHeldWhenTheChainEndsInAStall(t *testing.T) {
	t.Parallel()
	w := inReviewFailed(t)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"late"}, Needs: []string{"s1-1"}}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"later"}, Needs: []string{"late"}}))
	w.s.Open = nil
	f := mustStall(t, running(w), "later", "waits on late, which is stalled")
	if f.Root != "s1-1" {
		t.Fatalf("the root of the chain is s1-1, not %q", f.Root)
	}
	// One judgment for the root: the tick's check writes it, and names none
	// of the cards that wait behind it.
	p, _ := TickCheck(w.s, TickReq{})
	var stalled []Note
	for _, n := range p.Notes {
		if n.Type == NStalled {
			stalled = append(stalled, n)
		}
	}
	if len(stalled) != 1 || stalled[0].Primaries[0] != "s1-1" {
		t.Fatalf("the check's stall judgments: %+v", stalled)
	}
}

func TestNotHeldInACycle(t *testing.T) {
	t.Parallel()
	w := setup(t, 0)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"y0"}}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"x"}, Needs: []string{"y0"}}))
	// add refuses a cycle; one made by a writer outside the sprint holds
	// nothing.
	y0 := w.s.Work.Card("y0")
	y0.Col, y0.Fields["needs"] = Waiting, "x"
	w.s.Work.Put(y0)
	mustStall(t, running(w), "x", "stalled")
	mustStall(t, running(w), "y0", "stalled")
	got := Unheld(running(w), w.s.Now)
	if len(got) != 2 || got[0].Root != "y0" || !strings.Contains(got[1].Why, "its needs make a cycle through x") {
		t.Fatalf("the cycle's findings: %+v", got)
	}
}

func TestHeldByTheTickWhileTheMachineIsStopped(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	hd := mustHold(t, stopped(w), "s1-1", HeldByStopped)
	if !strings.Contains(hd.Why, "the machine is STOPPED") {
		t.Fatalf("stopped: %s", hd)
	}
}

func TestAStallStaysAStallWhileTheMachineIsStopped(t *testing.T) {
	t.Parallel()
	w := inReviewFailed(t)
	w.s.Open = nil
	mustStall(t, stopped(w), "s1-1", "no judgment is open on it")
}

func TestTheOtherStalls(t *testing.T) {
	t.Parallel()
	// A reached sentinel with no open judgment.
	w := setup(t, 0)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true}))
	w.s.Open = nil
	f := mustStall(t, running(w), "stop", "reached, and no judgment is open on it")
	if !contains(f.Decisions, "release") {
		t.Fatalf("a reached sentinel's decisions: %v", f.Decisions)
	}
	// A stopped stream with no open judgment.
	w = setup(t, 1)
	w.s.StreamCtl("s1").Fields["state"], w.s.StreamCtl("s1").Fields["cause"] = StreamStopped, "conflict"
	found := false
	for _, f := range Unheld(running(w), w.s.Now) {
		found = found || f.Subject == StreamSubject("s1") && strings.Contains(f.Why, "no judgment is open")
	}
	if !found {
		t.Fatalf("a stopped stream with no judgment is not a stall: %v", Unheld(running(w), w.s.Now))
	}
	// An operation pending past its grace that a tick since did not finish;
	// one in its grace, or with no tick since, is not.
	op := &PendingOp{ID: "op-1", Verb: "accept", At: t0}
	h := HeldState{Snap: w.s, Running: true, Pending: op, Grace: time.Minute, LastTick: t0.Add(30 * time.Second)}
	if got := Unheld(h, t0.Add(2*time.Minute)); len(got) != 0 {
		t.Fatalf("no tick since the grace: %v", got)
	}
	h.LastTick = t0.Add(90 * time.Second)
	if got := Unheld(h, t0.Add(2*time.Minute)); len(got) != 1 || !strings.Contains(got[0].What, "op-1") {
		t.Fatalf("pending past its grace: %v", got)
	}
	// A judgment past its due time: the next tick marks it overdue, so it is
	// held; marked, it is held by the mark.
	w = inReviewFailed(t)
	w.tick(DeadlineJudgment + time.Minute)
	if got := Unheld(running(w), w.s.Now); len(got) != 0 {
		t.Fatalf("an overdue judgment the tick marks: %v", got)
	}
	p, _ := TickOverdue(w.s, TickReq{})
	w.do(p)
	for _, n := range p.Notes {
		if n.Kind == Acknowledged {
			w.s.Acked = append(w.s.Acked, Open{Key: OpenKey(n.What, "s1-1"), Note: n})
		}
	}
	if got := Unheld(running(w), w.s.Now); len(got) != 0 {
		t.Fatalf("an overdue judgment marked: %v", got)
	}
}

func TestTheCheckWritesAStallOnceAndClosesItWhenItClears(t *testing.T) {
	t.Parallel()
	w := inReviewFailed(t)
	w.s.Open = nil
	p, _ := TickCheck(w.s, TickReq{})
	w.must(p)
	if got := w.notesOf(NStalled); len(got) != 1 || got[0].Kind != Judgment || !strings.Contains(got[0].What, "s1-1 review") {
		t.Fatalf("one stall judgment: %+v", got)
	}
	if p, _ := TickCheck(w.s, TickReq{}); !p.Empty() {
		t.Fatalf("written again: %+v", p)
	}
	// The coordinator reworks it: the rework closes the stall on it.
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "fix"}))
	if len(w.openOn("s1-1")) != 0 {
		t.Fatalf("the rework left open: %v", w.openOn("s1-1"))
	}
	if p, _ := TickCheck(w.s, TickReq{}); !p.Empty() {
		t.Fatalf("the check after the rework: %+v", p)
	}
}

// A stall's decisions are only those that would be accepted (reader finding
// 6): a primary in review already asked is offered ask --another, never
// ask, which would be refused as asked already.
func TestAStallOffersOnlyEnabledDecisions(t *testing.T) {
	t.Parallel()
	w := dealt(t)
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1")}))
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	reads := w.s.Readers.Of("s1-1")
	w.must(Read(w.s, ReadReq{As: reads[0].Row, Verdict: "broken", Finding: "f", Sel: Sel{IDs: []string{reads[0].ID}}}))
	w.must(Read(w.s, ReadReq{As: reads[1].Row, Verdict: "ok", Sel: Sel{IDs: []string{reads[1].ID}}}))
	w.s.Open = nil // the broken read's judgment closed without the step that writes what it needs next
	f := mustStall(t, running(w), "s1-1", "")
	if contains(f.Decisions, "ask") || !contains(f.Decisions, "ask --another") {
		t.Fatalf("asked already: %v", f.Decisions)
	}
	for _, d := range f.Decisions {
		if d == "wait" || d == "drop" || d == "rework" || d == "ask --another" {
			continue
		}
		t.Errorf("a decision not expected: %s", d)
	}
}

// "Stopped with moves due" counts the asks the tick would make (reader
// finding 6): a primary in review never asked at its attempt is a move due.
func TestMovesDueCountsAsks(t *testing.T) {
	t.Parallel()
	w := dealt(t)
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1")}))
	if n := MovesDue(w.s); n != 1 {
		t.Fatalf("a primary in review never asked: %d moves due", n)
	}
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	if n := MovesDue(w.s); n != 0 {
		t.Fatalf("asked: %d moves due", n)
	}
}
