package swarm

import (
	"testing"
)

// 037d8222 exported DecideFinished, RequeueOnce and TaskDecision so pulse
// can triage after a harvest. Reverting decide.go kept this package green:
// triage tests still drove decideOne, and the new names had no caller here.
func TestDecideFinishedReturnsTheTypedDecision(t *testing.T) {
	p, id := decideTestPool(t, "starting up\nInternal server error\n")
	out, err := DecideFinished(p, decideFake(nil), 0.9, decideTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("decisions = %d, want 1: %+v", len(out), out)
	}
	d := out[0]
	if d.Task != id || d.Label != "task" {
		t.Fatalf("task=%q label=%q, want id=%s label=task", d.Task, d.Label, id)
	}
	if d.Reason != "provider_error" || d.Confidence != 0.93 || d.NeedsHuman != 0.12 {
		t.Fatalf("typed fields wrong: %+v", d)
	}
	again, err := DecideFinished(p, decideFake(nil), 0.9, decideTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("a second pass must not ask again, got %d: %+v", len(again), again)
	}
}

func TestRequeueOnceRetriesAFinishedTaskAndRefusesASecondTime(t *testing.T) {
	p, id := decideTestPool(t, "Internal server error\n")
	next, ok := p.RequeueOnce(id, decideTestNow())
	if !ok {
		t.Fatal("a finished task with no requeue is retried once")
	}
	if next.Requeued != 1 || next.From != id {
		t.Fatalf("requeued sidecar: %+v", next)
	}
	pending, err := p.List(Pending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != next.ID {
		t.Fatalf("the retry sits in pending/: %+v", pending)
	}
	if _, ok := p.RequeueOnce(next.ID, decideTestNow()); ok {
		t.Fatal("a task already carrying a requeue is refused")
	}
}
