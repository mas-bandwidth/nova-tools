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
