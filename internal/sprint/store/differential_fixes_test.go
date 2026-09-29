package store

// The engine defects the differential test found against the reference
// model, each as the sequence that showed it.

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// 1. A cross need is gone with a return: a later conflict stop resumes by
// what was done, whatever the card once needed.
func TestACrossNeedDoesNotSurviveAReturn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"x"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"y"}}))
	h.through("x")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1, Cross: "x=y"}))
	h.must(ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: []string{"x"}}, Reason: "cross wait"}))
	h.must(ResumeStep(sprint.ResumeReq{Stream: "s1", Did: "d"}))
	h.must(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{"x"}}}))
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1, Conflict: "x"}))
	res := h.run(ResumeStep(sprint.ResumeReq{Stream: "s1", Did: "rebased x"}))
	if len(res.Refused) != 0 || h.snap().StreamCtl("s1").F("state") != sprint.StreamMerging {
		t.Fatalf("resume after a conflict: %+v", res)
	}
	h.clean("resumed")
}

// 2. Releasing a stream's only landed card lands the stream: settle counts
// from the state after the step.
func TestReleasingTheOnlyCardLandsItsStream(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if err := h.m.SetCoordinator(h.ctx, "tester"); err != nil {
		t.Fatal(err)
	}
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"p2"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"p4"}, Sentinel: true, Before: "p2"}))
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"p2"}}, Reason: "gone"}))
	h.must(ReleaseStep(sprint.ReleaseReq{IDs: []string{"p4"}, Reason: "done", Coordinator: "tester", Who: "tester"}))
	if st := h.snap().StreamCtl("s3").F("state"); st != sprint.StreamLanded {
		t.Fatalf("s3 is %s after its only card landed", st)
	}
	h.clean("released")
}

// 3. A sprint whose every card was dropped is done, 0 landed; a stream with
// every primary dropped is empty (waiting, no since), never landed.
func TestAnAllDroppedSprintIsDoneAndItsStreamEmpty(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"p1"}}))
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"p1"}}, Reason: "gone"}))
	open := h.openOf(sprint.NSprintDone)
	if len(open) != 1 || open[0].Note.What != "0 landed, 1 dropped" {
		t.Fatalf("the sprint is done: %+v", open)
	}
	if c := h.snap().StreamCtl("s3"); c.F("state") != sprint.StreamWaiting || c.F("since") != "" {
		t.Fatalf("the empty stream: %v", c.Fields)
	}
	h.clean("all dropped")
}
