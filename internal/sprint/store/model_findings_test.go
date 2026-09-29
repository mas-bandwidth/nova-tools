package store

// The TLA+ model's findings (tla/SprintTables.tla, the owner's rule), each as
// the model's trace run through the store.

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// openOn is the open judgments on a primary.
func (h *harness) openOn(id string) []sprint.Open {
	h.t.Helper()
	open, err := h.m.OpenNotes(h.ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []sprint.Open
	for _, o := range open {
		if o.Subject() == id && o.Note.Kind == sprint.Judgment {
			out = append(out, o)
		}
	}
	return out
}

// toReview deals, takes and finishes s1-1 ok, then asks it.
func (h *harness) toReview() {
	h.t.Helper()
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	c := h.snap().Fleet.Card("s1-1.w1")
	h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: 1}}))
	h.must(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: 1}}))
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
}

// M1: deal, take, finish ok, ask, both reads ok (ready to accept opens), ack:
// refused (ack answers only a judgment that lists it), naming the commands
// that move the card; the judgment stays open.
func TestModelAckCannotSilenceACard(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.toReview()
	for _, rc := range h.snap().Readers.Of("s1-1") {
		h.must(ReadStep(sprint.ReadReq{As: rc.F("reader"), Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc.ID}}}))
	}
	open := h.openOn("s1-1")
	if len(open) != 1 || open[0].Note.Type != sprint.NReadyToAccept {
		t.Fatalf("after two ok reads: %+v", open)
	}
	res := h.run(AckStep(sprint.AckReq{Notes: []string{open[0].Note.ID}, Reason: "looked"}))
	if len(res.Moved) != 0 || len(res.Refused) != 1 || !strings.Contains(res.Refused[0].Why, "ack does not answer") ||
		!strings.Contains(res.Refused[0].Why, "nova-sprint accept --group "+open[0].Note.ID) {
		t.Fatalf("the ack that silences s1-1: %+v", res)
	}
	if again := h.openOn("s1-1"); len(again) != 1 || again[0].Note.ID != open[0].Note.ID {
		t.Fatalf("after the refused ack: %+v", again)
	}
	// Returned to review: ack does not answer it either.
	h.must(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	h.must(ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "hold it"}))
	ret := h.openOn("s1-1")
	if len(ret) != 1 || ret[0].Note.Type != sprint.NReturned {
		t.Fatalf("returned: %+v", ret)
	}
	if res := h.run(AckStep(sprint.AckReq{Notes: []string{ret[0].Note.ID}, Reason: "looked"})); len(res.Refused) != 1 || len(res.Moved) != 0 {
		t.Fatalf("ack of returned: %+v", res)
	}
	if held := h.openOn("s1-1"); len(held) != 1 || held[0].Note.Type != sprint.NReturned {
		t.Fatalf("returned, after the refused ack: %+v", held)
	}
	h.clean("M1")
}

// M1, blocked: the ack waives the dropped need and the card moves to ready,
// where the tick deals it: held, so not refused.
func TestModelAckOfBlockedMovesTheCard(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"late"}, Needs: []string{"s1-1"}}))
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "gone"}))
	open := h.openOn("late")
	if len(open) != 1 || open[0].Note.Type != sprint.NBlocked {
		t.Fatalf("blocked: %+v", open)
	}
	h.must(AckStep(sprint.AckReq{Notes: []string{open[0].Note.ID}, Reason: "not needed"}))
	if h.state("late") != sprint.Ready {
		t.Fatalf("late is %s after the waiver", h.state("late"))
	}
	h.clean("M1 blocked")
}

// The second audit's gap, inverted (TestAudit2GapAckedSentinelStopsItsStreamForEver):
// an ack of "sentinel reached" is refused, naming release; the sentinel's
// judgment stays open, and release moves what waits behind it.
func TestAckOfAReachedSentinelIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if err := h.m.SetCoordinator(h.ctx, "tester"); err != nil {
		t.Fatal(err)
	}
	h.setup(1)
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"after"}}))
	h.startMachine()
	h.machine()
	h.work("m1")
	h.work("m2")
	h.machine()
	h.readAll()
	h.landAll("s1")
	reached := h.openOf(sprint.NSentinelReached)
	if h.state("s1-1") != sprint.Landed || len(reached) != 1 {
		t.Fatalf("s1-1 %s, reached %d", h.state("s1-1"), len(reached))
	}
	res := h.run(AckStep(sprint.AckReq{Notes: []string{reached[0].Note.ID}, Reason: "looked"}))
	if len(res.Moved) != 0 || len(res.Refused) != 1 || !strings.Contains(res.Refused[0].Why, "nova-sprint release stop") {
		t.Fatalf("ack of sentinel reached: %+v", res)
	}
	for i := 0; i < 5; i++ {
		h.tick(time.Hour)
		h.machine()
	}
	if len(h.openOf(sprint.NSentinelReached)) != 1 || h.state("stop") != sprint.Waiting || h.state("after") != sprint.Waiting {
		t.Fatalf("after the refused ack: reached %d, stop %s, after %s", len(h.openOf(sprint.NSentinelReached)), h.state("stop"), h.state("after"))
	}
	h.must(ReleaseStep(sprint.ReleaseReq{IDs: []string{"stop"}, Reason: "green", Coordinator: "tester", Who: "tester", Answers: []string{reached[0].Note.ID}}))
	h.machine()
	if h.state("stop") != sprint.Landed || h.state("after") != sprint.Working {
		t.Fatalf("after the release: stop %s after %s", h.state("stop"), h.state("after"))
	}
	h.clean("released")
}

// M2: the no-member judgment is kept true by the tick in every epoch: both
// members down, clear, start, add a card, tick: the judgment is open.
func TestModelNoMemberJudgmentAfterAClear(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "m1"}))
	h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "m2"}))
	h.machine()
	if len(h.openOf(sprint.NNoMember)) != 1 {
		t.Fatalf("no member before the clear: %d", len(h.openOf(sprint.NNoMember)))
	}
	if _, err := h.st.Clear(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.startMachine()
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"p3"}}))
	h.machine()
	pinned, err := h.st.Pinned(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	all, err := pinned.B.OpenNotes(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var open []sprint.Open
	for _, o := range all {
		if o.Note.Type == sprint.NNoMember && o.Note.Kind == sprint.Judgment {
			open = append(open, o)
		}
	}
	if pinned.PinnedEpoch() != 1 || len(open) != 1 || sprint.IDEpoch(open[0].Note.ID) != 1 || h.state("p3") != sprint.Ready {
		t.Fatalf("after the clear: open %+v, p3 %s", open, h.state("p3"))
	}
	h.clean("M2")
}

// M3: ask --another before the first ask of the primary is refused, naming
// ask (or the tick) as what asks first.
func TestModelAskAnotherBeforeTheFirstAsk(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	c := h.snap().Fleet.Card("s1-1.w1")
	h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: 1}}))
	h.must(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: 1}}))
	res := h.run(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Another: true}))
	if len(res.Moved) != 0 || len(res.Refused) != 1 || !strings.Contains(res.Refused[0].Why, "nova-sprint ask s1-1") || !strings.Contains(res.Refused[0].Why, "tick") {
		t.Fatalf("ask --another before the first ask: %+v", res)
	}
	if n := len(h.snap().Readers.Of("s1-1")); n != 0 {
		t.Fatalf("asked of %d readers", n)
	}
}
