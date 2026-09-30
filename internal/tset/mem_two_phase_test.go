package tset

import (
	"context"
	"reflect"
	"testing"
)

func twoPhaseFixture(t *testing.T) *Mem {
	t.Helper()
	m := NewMem()
	if err := m.DefineTable("two-phase:", "work", TableDefinition{
		Columns: []string{"c"}, MemberPrefix: "two-phase:member:work:",
		EpochKey: "two-phase:sprint:epoch", EpochField: "n",
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.SeedRow("two-phase:", "work", "0", "r", "0"); err != nil {
		t.Fatal(err)
	}
	return m
}

func twoPhaseCreate() Step {
	op, intent := "create-op", "create-intent"
	return Step{Epoch: "0", Space: "two-phase:", Op: &op, Intent: &intent, Entries: []Entry{{
		Kind: "create", Table: "work", To: "r:c", IDs: []string{"one"}, Scores: []string{"2"},
		Set: map[string]string{"status": "ready"},
	}}}
}

func TestMemTwoPhasePlanDoesNotWriteAndCommitUsesPrivateCandidate(t *testing.T) {
	t.Parallel()
	m := twoPhaseFixture(t)
	step := twoPhaseCreate()
	before, err := m.Snapshot(step.Space)
	if err != nil {
		t.Fatal(err)
	}
	plan, refusal := m.Plan(step)
	if refusal != nil || plan == nil || len(plan.Entries) != 1 || len(plan.Entries[0].After) != 1 {
		t.Fatalf("plan: %+v refusal=%v", plan, refusal)
	}
	between, err := m.Snapshot(step.Space)
	if err != nil || !reflect.DeepEqual(before, between) {
		t.Fatalf("Plan wrote state: err=%v", err)
	}
	// The public observation and caller's input are deliberately mutable.
	// Commit must use its separate captured candidate and reply.
	plan.Entries[0].Entry.IDs[0] = "other"
	plan.Entries[0].After[0].Fields["status"] = FieldValue{Present: true, Value: "wrong"}
	plan.Entries[0].FieldChanges[0].Set["status"] = "wrong"
	plan.Before["work"]["one"] = MemberRecord{ID: "other"}
	step.Entries[0].IDs[0] = "other"
	step.Entries[0].Set["status"] = "wrong"
	reply, refusal := m.Commit(plan)
	if refusal != nil || reply.Status != "ok" || reply.Changed != 1 || reply.MemPlan == nil ||
		reply.MemPlan.Entries[0].Entry.IDs[0] != "one" ||
		reply.MemPlan.Entries[0].After[0].Fields["status"].Value != "ready" {
		t.Fatalf("commit used caller mutation: reply=%+v refusal=%v", reply, refusal)
	}
	after, err := m.Snapshot("two-phase:")
	if err != nil {
		t.Fatal(err)
	}
	member := after.Epochs["0"].Tables["work"].Records["one"]
	if member.Fields["status"] != "ready" || after.Epochs["0"].Tables["work"].Records["other"].Epoch != "" ||
		after.Receipts["0"]["create-op"].IntentDigest == "" {
		t.Fatalf("commit did not publish exact candidate: %+v", after)
	}
	if _, refusal := m.Commit(plan); refusal == nil || refusal.Code != "REQUEST" {
		t.Fatalf("reused plan: %v", refusal)
	}
}

func TestMemTwoPhaseRejectsMovedAndForeignState(t *testing.T) {
	t.Parallel()
	m := twoPhaseFixture(t)
	step := twoPhaseCreate()
	plan, refusal := m.Plan(step)
	if refusal != nil {
		t.Fatal(refusal)
	}
	other := twoPhaseFixture(t)
	if _, refusal := other.Commit(plan); refusal == nil || refusal.Code != "REQUEST" {
		t.Fatalf("foreign plan: %v", refusal)
	}
	if _, refusal := m.Commit(nil); refusal == nil || refusal.Code != "REQUEST" {
		t.Fatalf("nil plan: %v", refusal)
	}
	// SeedRow mutates the same memNamespace pointer; the version check, not only
	// pointer identity, must reject the prepared candidate.
	if err := m.SeedRow(step.Space, "work", "0", "later", "1"); err != nil {
		t.Fatal(err)
	}
	before, err := m.Snapshot(step.Space)
	if err != nil {
		t.Fatal(err)
	}
	if _, refusal := m.Commit(plan); refusal == nil || refusal.Code != "REVISION" || refusal.Detail.ActiveEpoch != "0" {
		t.Fatalf("moved fixture state: %v", refusal)
	}
	after, err := m.Snapshot(step.Space)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("stale commit changed state: err=%v", err)
	}
	if _, refusal := m.Commit(plan); refusal == nil || refusal.Code != "REQUEST" {
		t.Fatalf("reused stale plan: %v", refusal)
	}
	// A successful Step replaces the namespace pointer, including for a no-op.
	newPlan, refusal := m.Plan(step)
	if refusal != nil {
		t.Fatal(refusal)
	}
	if _, err := m.Step(context.Background(), Step{Epoch: "0", Space: step.Space, Entries: []Entry{}}); err != nil {
		t.Fatal(err)
	}
	if _, refusal := m.Commit(newPlan); refusal == nil || refusal.Code != "REVISION" {
		t.Fatalf("intervening Step: %v", refusal)
	}
}

func TestMemTwoPhaseReplayFenceAndAbsentSpace(t *testing.T) {
	t.Parallel()
	m := NewMem()
	if plan, refusal := m.Plan(Step{Epoch: "0", Space: "absent:", Entries: []Entry{}}); plan != nil ||
		refusal == nil || refusal.Code != "CONFIG" || len(m.spaces) != 0 {
		t.Fatalf("absent Plan created a space: plan=%+v refusal=%v spaces=%d", plan, refusal, len(m.spaces))
	}
	m = twoPhaseFixture(t)
	step := twoPhaseCreate()
	if _, err := m.Step(context.Background(), step); err != nil {
		t.Fatal(err)
	}
	replay, refusal := m.Plan(step)
	if refusal != nil || replay == nil || replay.prepared == nil || !replay.Replay {
		t.Fatalf("replay Plan handle: %+v refusal=%v", replay, refusal)
	}
	replay.Replay = false // Public observation does not control the captured commit.
	beforeNamespace, beforeVersion := m.spaces[step.Space], m.spaces[step.Space].version
	before, err := m.Snapshot(step.Space)
	if err != nil {
		t.Fatal(err)
	}
	reply, refusal := m.Commit(replay)
	if refusal != nil || !reply.Replay || reply.MemPlan != nil ||
		m.spaces[step.Space] != beforeNamespace || m.spaces[step.Space].version != beforeVersion {
		t.Fatalf("replay republished state: reply=%+v refusal=%v", reply, refusal)
	}
	fenceOp, fenceIntent := "fence-op", "fence-intent"
	fence := Step{Epoch: "0", Space: step.Space, Op: &fenceOp, Intent: &fenceIntent,
		Fence: true, Entries: []Entry{}}
	fencePlan, refusal := m.Plan(fence)
	if refusal != nil || fencePlan == nil {
		t.Fatalf("fence Plan: %+v refusal=%v", fencePlan, refusal)
	}
	between, err := m.Snapshot(step.Space)
	if err != nil || !reflect.DeepEqual(before, between) {
		t.Fatalf("fence Plan wrote receipt: err=%v", err)
	}
	reply, refusal = m.Commit(fencePlan)
	if refusal != nil || reply.Status != "fenced" || reply.MemPlan != nil {
		t.Fatalf("fence Commit: reply=%+v refusal=%v", reply, refusal)
	}
	after, err := m.Snapshot(step.Space)
	if err != nil || after.Receipts["0"][fenceOp].Status != "fenced" {
		t.Fatalf("fence receipt missing: snapshot=%+v err=%v", after, err)
	}
}
