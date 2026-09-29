package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func (h *harness) lines() []sprint.Line {
	h.t.Helper()
	ls, err := h.st.Log(h.ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	return ls
}

// linesOf is the rendered lines about a card, in order.
func (h *harness) linesOf(id string) []string {
	var out []string
	for _, l := range h.lines() {
		if l.About(id) {
			out = append(out, sprint.Render(l))
		}
	}
	return out
}

// The log: every change of every card is a line written with the step, the
// machine's own moves (a redeal when a member goes silent, a level move)
// among them; replaying it gives every card's place (rule 13, which check
// holds after every step here).
func TestTheLogHoldsEveryMoveAndReplaysToTheTables(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.live = []string{"m1", "m2"}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"p1"}, Brief: "do the thing"}))
	h.startMachine()
	h.machine() // p1 dealt to m1
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	h.live = []string{"m2"} // m1 silent: its card is taken back and redealt
	for i := 0; i < 3; i++ {
		h.tick(10 * time.Second)
		h.machine()
	}
	h.clean("redealt")
	c := h.snap().Fleet.Card("p1.w1")
	if c == nil || c.Row != "m2" {
		t.Fatalf("redealt to m2: %+v", c)
	}
	h.run(TakeStep(sprint.TakeReq{As: "m2", Sel: sprint.Sel{IDs: []string{"p1.w1"}}, Gens: map[string]int{"p1.w1": c.Int("gen")}, Who: "m2"}))
	h.must(FinishStep(sprint.FinishReq{As: "m2", Sel: sprint.Sel{IDs: []string{"p1.w1"}}, Gens: map[string]int{"p1.w1": c.Int("gen")}, Failed: true, Report: "the tests went red", Who: "m2"}))
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"p1"}}, Answers: []string{h.openOf(sprint.NWorkFailed)[0].Note.ID}}))
	h.clean("reworked")
	got := strings.Join(h.linesOf("p1"), "\n")
	for _, want := range []string{
		"p1 added to s1 by tester, score",
		"attempt 1 dealt to m1",
		"attempt 1 redealt from m1 to m2 by the machine",
		"m2 took attempt 1",
		"m2 finished attempt 1: FAILED",
		"judgment: work came back failed",
		"p1 reworked by tester: attempt 2",
		"answered \"work came back failed\" by tester",
		"attempt 2 dealt to",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the log of p1 has no %q:\n%s", want, got)
		}
	}
	var report, brief bool
	for _, l := range h.lines() {
		report = report || l.Text["report"] == "the tests went red"
		brief = brief || l.Text["brief"] == "do the thing"
	}
	if !report || !brief {
		t.Fatalf("the words given are not on their lines: report %v, brief %v", report, brief)
	}
	if v := sprint.LogViolations(h.snap(), h.lines()); len(v) != 0 {
		t.Fatalf("the log does not replay: %v", v)
	}
}

// Rule 13 holds the log to the tables: a card moved by a writer outside the
// steps, with no line, is a violation naming it.
func TestAMoveTheLogNeverSawIsRule13(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	s := h.snap()
	wc := s.Fleet.Card("s1-1.w1")
	if _, err := h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-fleet", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Fleet.Revision),
		OperationID: "outside-move", Members: []ntable.BatchMemberEntry{{ID: wc.ID, Expect: &ntable.MemberExpect{Revision: fmt.Sprint(wc.Rev)},
			Move: &ntable.MemberMoveOp{Row: wc.Row, Col: sprint.Withdrawn}}}}); err != nil {
		t.Fatal(err)
	}
	rep, _, err := h.st.Check(h.ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range rep.Violations {
		found = found || v.Rule == 13 && strings.Contains(v.Detail, "s1-1.w1")
	}
	if !found {
		t.Fatalf("an unlogged move: %v", rep.Violations)
	}
}

// A clear keeps the old epoch's log, readable at that epoch, and the new
// epoch's log starts empty.
func TestAClearKeepsTheOldEpochsLog(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	before := len(h.lines())
	if before == 0 {
		t.Fatalf("no lines before the clear")
	}
	if _, err := h.st.Clear(h.ctx); err != nil {
		t.Fatal(err)
	}
	st, err := h.st.Pinned(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	now, err := st.Log(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range now {
		if l.Kind == sprint.LineMove && !strings.HasSuffix(l.To, ":"+sprint.Ctl) {
			t.Fatalf("the new epoch's log has a move line: %+v", l)
		}
	}
	old, err := st.At(0).Log(h.ctx)
	if err != nil || len(old) != before {
		t.Fatalf("the old epoch's log: %d lines, want %d (%v)", len(old), before, err)
	}
}
