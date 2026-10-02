package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// The no-stall rule, one state per holder: each holder holding, and each
// failing, built by hand on the core's own world.

func running(w *world) HeldState { return HeldState{Snap: w.s, Running: true} }

func stopped(w *world) HeldState { return HeldState{Snap: w.s} }

func mustHold(t *testing.T, h HeldState, id, by string) Hold {
	t.Helper()
	hd := Holder(h, h.Snap.Now, id)
	require.Equal(t, by, hd.By, "%s is held by %q, want %q: %s", id, hd.By, by, hd)
	for _, f := range Unheld(h, h.Snap.Now) {
		require.NotEqual(t, id, f.Subject, "%s is held (%s) and Unheld names it: %s", id, hd, f)
	}
	return hd
}

func mustStall(t *testing.T, h HeldState, id, why string) Finding {
	t.Helper()
	hd := Holder(h, h.Snap.Now, id)
	require.True(t, hd.Stalled(), "%s: %s, want stalled: ...%s...", id, hd, why)
	require.Contains(t, hd.Why, why, "%s: %s, want stalled: ...%s...", id, hd, why)
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
	require.Contains(t, hd.Why, "s1-1.w1@1", "the holder does not name the card and its generation: %s", hd)
	// Past the deadline the actor no longer holds it: the next tick writes the
	// late judgment, and once written the judgment holds it.
	w.tick(DeadlineUnfinished + time.Minute)
	hd = mustHold(t, running(w), "s1-1", HeldByTick)
	require.Contains(t, hd.Why, NWorkLate, "past its deadline: %s", hd)
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
	require.Empty(t, f.Root, "the finding: %+v", f)
	require.Contains(t, f.Decisions, "fleet down "+wc.Row, "the finding: %+v", f)
	require.Contains(t, f.Decisions, "drop", "the finding: %+v", f)
}

func TestHeldByTheNextTick(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	hd := mustHold(t, running(w), "s1-1", HeldByTick)
	require.Contains(t, hd.Why, "working card=s1-1.w1", "the next tick deals it: %s", hd)
}

func TestNotHeldWhenTheTickCannotDealIt(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	// A card of the next attempt exists already (a writer outside the
	// sprint): deal refuses it, and with room in the queues it waits for
	// nothing.
	w.s.Fleet.Put(&Card{ID: "s1-1.w1", Row: "m1", Col: Done, Score: 1, Rev: 1, Fields: map[string]string{"kind": "work", "primary": "s1-1"}})
	mustStall(t, running(w), "s1-1", "a member is below its room (DealAhead times its width), and nothing deals it")
}

// refusedAtStaging is two members of width 1, x and y, and s1-1's card refused at staging by
// x, so s1-1 is ready again: x works s1-3 with one place free (no lane free, so the level
// sends it nothing), and y, the only member s1-1 may go to, is at its room (s1-2 working,
// s1-4 ready behind it). It is the state of the live run of 2026-10-01 whose check raised
// "stalled" with nothing wrong: the refuser below its room, the card dealt to the other member
// a tick later.
func refusedAtStaging(t *testing.T, primaries int) (w *world, x, y string) {
	t.Helper()
	w = fleetWorld(t, primaries, 1, "m1", "m2")
	dealTo := func(id string) string {
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}}))
		return w.s.Fleet.Card(id + ".w1").Row
	}
	x = dealTo("s1-1")
	takeCard(w, "s1-1.w1")
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1"), Failed: true,
		Report: cardhdr.EndStaging + ": no bench mirror; no child ran"}))
	require.Equal(t, Ready, w.state("s1-1"))
	require.Equal(t, []string{x}, StagingRefusers(w.s.Fleet.Card("s1-1.w1")))
	y = dealTo("s1-2")
	require.NotEqual(t, x, y)
	takeCard(w, "s1-2.w1")
	require.Equal(t, x, dealTo("s1-3"))
	takeCard(w, "s1-3.w1")
	require.Equal(t, y, dealTo("s1-4"))
	require.Equal(t, DealAhead*1, widthHeld(w, y))
	require.Equal(t, 1, widthHeld(w, x))
	return w, x, y
}

// A ready card whose only free place is on a member that refused it at staging waits for a
// member it may go to: the deal refuses it (every member it may go to is at its room), and the
// check counts only the places it may have, so it raises no stall; the tick deals it once that
// member has room. Counting the refuser's place (the check before the fix) raises "stalled".
func TestARefuserBelowItsRoomDoesNotStallACardItRefused(t *testing.T) {
	t.Parallel()
	w, x, y := refusedAtStaging(t, 4)
	hd := mustHold(t, running(w), "s1-1", HeldByWaiting)
	assert.Contains(t, hd.Why, "waits for a member below its room")
	p, _ := TickCheck(w.s, TickReq{})
	w.must(p)
	assert.Empty(t, w.notesOf(NStalled), "a card the deal places when a member it may go to has room is no stall")

	dp, _ := TickDeal(w.s, TickReq{})
	require.Len(t, dp.Refused, 1)
	assert.Contains(t, dp.Refused[0].Why, noRoomWhy)
	w.do(dp)
	assert.Equal(t, Ready, w.state("s1-1"), "never dealt to the member that refused it")
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-2.w1"}}, Gens: gensOf(w.s, "s1-2.w1")}))
	w.part(TickDeal, TickReq{})
	assert.Equal(t, Working, w.state("s1-1"))
	assert.Equal(t, y, w.s.Fleet.Card("s1-1.w1").Row, "dealt to the member that has not refused it, not %s", x)
}

// The refuser's room still counts for every other card: a ready card the deal cannot place
// (a card of its next attempt exists already, a writer outside the sprint) while the refuser
// is below its room is a stall, and the check raises it, beside the card that waits.
func TestARefuserBelowItsRoomStillStallsACardItMayTake(t *testing.T) {
	t.Parallel()
	w, x, _ := refusedAtStaging(t, 5)
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-3.w1"}}, Gens: gensOf(w.s, "s1-3.w1")}))
	require.Zero(t, widthHeld(w, x))
	w.s.Fleet.Put(&Card{ID: "s1-5.w1", Row: x, Col: Done, Score: 5, Rev: 1, Fields: map[string]string{"kind": "work", "primary": "s1-5"}})
	mustHold(t, running(w), "s1-1", HeldByWaiting)
	mustStall(t, running(w), "s1-5", "a member is below its room (DealAhead times its width), and nothing deals it")
	p, _ := TickCheck(w.s, TickReq{})
	w.must(p)
	got := w.notesOf(NStalled)
	require.Len(t, got, 1)
	assert.Equal(t, []string{"s1-5"}, got[0].Primaries)
}

func TestHeldByAnOpenJudgment(t *testing.T) {
	t.Parallel()
	w := inReviewFailed(t)
	hd := mustHold(t, running(w), "s1-1", HeldByJudgment)
	require.Contains(t, hd.Why, NWorkFailed, "held by the failed judgment: %s", hd)
}

func TestNotHeldWhenTheJudgmentIsGone(t *testing.T) {
	t.Parallel()
	w := inReviewFailed(t)
	w.s.Open = nil // closed without the step that writes what it needs next
	f := mustStall(t, running(w), "s1-1", "its work came back failed, and no judgment is open on it")
	require.Contains(t, f.Decisions, "rework", "a failed primary's decisions: %v", f.Decisions)
	require.NotContains(t, f.Decisions, "ask", "a failed primary's decisions: %v", f.Decisions)
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
	require.Contains(t, hd.Why, "needs s1-1 (a)", "the chain: %s", hd)
	// Behind a reached sentinel whose judgment is open.
	w2 := setup(t, 0)
	w2.must(Add(w2.s, AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true}))
	w2.must(Add(w2.s, AddReq{Stream: "s1", IDs: []string{"after"}}))
	mustHold(t, running(w2), "stop", HeldByJudgment)
	hd = mustHold(t, running(w2), "after", HeldByWaiting)
	require.Contains(t, hd.Why, "waits behind sentinel stop (c)", "behind the sentinel: %s", hd)
}

func TestNotHeldWhenTheChainEndsInAStall(t *testing.T) {
	t.Parallel()
	w := inReviewFailed(t)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"late"}, Needs: []string{"s1-1"}}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"later"}, Needs: []string{"late"}}))
	w.s.Open = nil
	f := mustStall(t, running(w), "later", "waits on late, which is stalled")
	require.Equal(t, "s1-1", f.Root, "the root of the chain is s1-1, not %q", f.Root)
	// One judgment for the root: the tick's check writes it, and names none
	// of the cards that wait behind it.
	p, _ := TickCheck(w.s, TickReq{})
	var stalled []Note
	for _, n := range p.Notes {
		if n.Type == NStalled {
			stalled = append(stalled, n)
		}
	}
	require.Len(t, stalled, 1, "the check's stall judgments: %+v", stalled)
	require.Equal(t, "s1-1", stalled[0].Primaries[0], "the check's stall judgments: %+v", stalled)
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
	require.Len(t, got, 2, "the cycle's findings: %+v", got)
	require.Equal(t, "y0", got[0].Root, "the cycle's findings: %+v", got)
	require.Contains(t, got[1].Why, "its needs make a cycle through x", "the cycle's findings: %+v", got)
}

func TestHeldByTheTickWhileTheMachineIsStopped(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	hd := mustHold(t, stopped(w), "s1-1", HeldByStopped)
	require.Contains(t, hd.Why, "the machine is STOPPED", "stopped: %s", hd)
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
	require.Contains(t, f.Decisions, "release", "a reached sentinel's decisions: %v", f.Decisions)
	// A stopped stream with no open judgment.
	w = setup(t, 1)
	w.s.StreamCtl("s1").Fields["state"], w.s.StreamCtl("s1").Fields["cause"] = StreamStopped, "conflict"
	found := false
	for _, f := range Unheld(running(w), w.s.Now) {
		found = found || f.Subject == StreamSubject("s1") && strings.Contains(f.Why, "no judgment is open")
	}
	require.True(t, found, "a stopped stream with no judgment is not a stall: %v", Unheld(running(w), w.s.Now))
	// An operation pending past its grace that a tick since did not finish;
	// one in its grace, or with no tick since, is not.
	op := &PendingOp{ID: "op-1", Verb: "accept", At: t0}
	h := HeldState{Snap: w.s, Running: true, Pending: op, Grace: time.Minute, LastTick: t0.Add(30 * time.Second)}
	got := Unheld(h, t0.Add(2*time.Minute))
	require.Empty(t, got, "no tick since the grace: %v", got)
	h.LastTick = t0.Add(90 * time.Second)
	got = Unheld(h, t0.Add(2*time.Minute))
	require.Len(t, got, 1, "pending past its grace: %v", got)
	require.Contains(t, got[0].What, "op-1", "pending past its grace: %v", got)
	// A judgment past its due time: the next tick marks it overdue, so it is
	// held; marked, it is held by the mark.
	w = inReviewFailed(t)
	w.tick(DeadlineJudgment + time.Minute)
	got = Unheld(running(w), w.s.Now)
	require.Empty(t, got, "an overdue judgment the tick marks: %v", got)
	p, _ := TickOverdue(w.s, TickReq{})
	w.do(p)
	for _, n := range p.Notes {
		if n.Kind == Acknowledged {
			w.s.Acked = append(w.s.Acked, Open{Key: OpenKey(n.What, "s1-1"), Note: n})
		}
	}
	got = Unheld(running(w), w.s.Now)
	require.Empty(t, got, "an overdue judgment marked: %v", got)
}

func TestTheCheckWritesAStallOnceAndClosesItWhenItClears(t *testing.T) {
	t.Parallel()
	w := inReviewFailed(t)
	w.s.Open = nil
	p, _ := TickCheck(w.s, TickReq{})
	w.must(p)
	got := w.notesOf(NStalled)
	require.Len(t, got, 1, "one stall judgment: %+v", got)
	require.Equal(t, Judgment, got[0].Kind, "one stall judgment: %+v", got)
	require.Contains(t, got[0].What, "s1-1 review", "one stall judgment: %+v", got)
	p, _ = TickCheck(w.s, TickReq{})
	require.True(t, p.Empty(), "written again: %+v", p)
	// The coordinator reworks it: the rework closes the stall on it.
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "fix"}))
	require.Empty(t, w.openOn("s1-1"), "the rework left open")
	p, _ = TickCheck(w.s, TickReq{})
	require.True(t, p.Empty(), "the check after the rework: %+v", p)
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
	require.NotContains(t, f.Decisions, "ask", "asked already: %v", f.Decisions)
	require.Contains(t, f.Decisions, "ask --another", "asked already: %v", f.Decisions)
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
	n := MovesDue(w.s)
	require.Equal(t, 1, n, "a primary in review never asked: %d moves due", n)
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	n = MovesDue(w.s)
	require.Equal(t, 0, n, "asked: %d moves due", n)
}
