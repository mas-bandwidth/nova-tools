package store

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Judgments are the coordinator's: ack, wait and every --answers by another
// actor are refused, naming the coordinator; workers and readers keep their
// own verbs.
func TestOnlyTheCoordinatorAnswersAJudgment(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if err := h.m.SetCoordinator(h.ctx, "the-coordinator"); err != nil {
		t.Fatal(err)
	}
	h.setup(1)
	h.must(CIStep(sprint.CIReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Red: true, Run: "r1"}))
	open := h.openOf(sprint.NCIRed)
	if len(open) != 1 {
		t.Fatalf("ci red: %d", len(open))
	}
	id := open[0].Note.ID
	res := h.run(AckStep(sprint.AckReq{Notes: []string{id}, Reason: "looked", Who: "m1"}))
	if len(res.Refused) != 1 || !strings.Contains(res.Refused[0].Why, "the-coordinator") || len(h.openOf(sprint.NCIRed)) != 1 {
		t.Fatalf("ack by a worker: %+v", res)
	}
	res = h.run(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "x", Answers: []string{id}, Who: "reader-a"}))
	if len(res.Refused) != 1 || len(res.Moved) != 0 || !strings.Contains(res.Refused[0].Why, "the-coordinator") || h.state("s1-1") != sprint.Ready {
		t.Fatalf("drop --answers by a reader: %+v", res)
	}
	st := *h.st
	st.Actor = "m1"
	if _, _, err := st.Wait(h.ctx, id, h.now.Add(10*60*1e9)); err == nil || !strings.Contains(err.Error(), "the-coordinator") {
		t.Fatalf("wait by a worker: %v", err)
	}
	// the coordinator answers it
	h.must(AckStep(sprint.AckReq{Notes: []string{id}, Reason: "a flaky runner", Who: "the-coordinator"}))
	if len(h.openOf(sprint.NCIRed)) != 0 {
		t.Fatalf("the coordinator's ack did not close it")
	}
}
