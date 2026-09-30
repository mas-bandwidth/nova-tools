package tset

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func assertMemPlannedCounts(t *testing.T, plan *MemPlan, reply Reply) {
	t.Helper()
	var counters struct {
		Commands  int64 `json:"planned_commands"`
		ArgvBytes int64 `json:"planned_argv_bytes"`
	}
	if err := json.Unmarshal(reply.Counters, &counters); err != nil {
		t.Fatalf("decode planned counters: %v", err)
	}
	if plan.PlannedCommands != counters.Commands || plan.PlannedArgvBytes != counters.ArgvBytes {
		t.Fatalf("plan counts %d/%d differ from commit %d/%d", plan.PlannedCommands,
			plan.PlannedArgvBytes, counters.Commands, counters.ArgvBytes)
	}
}

func TestMemPlannedCountsMatchCommitAndIgnorePublicTampering(t *testing.T) {
	t.Parallel()
	m := twoPhaseFixture(t)
	step := twoPhaseCreate() // Named create includes the planned receipt HSET.
	before, err := m.Snapshot(step.Space)
	if err != nil {
		t.Fatal(err)
	}
	plan, refusal := m.Plan(step)
	if refusal != nil || plan == nil || plan.PlannedCommands <= 0 || plan.PlannedArgvBytes <= 0 {
		t.Fatalf("planned counts: plan=%+v refusal=%v", plan, refusal)
	}
	between, err := m.Snapshot(step.Space)
	if err != nil || !reflect.DeepEqual(before, between) {
		t.Fatalf("Plan changed state: err=%v", err)
	}
	commands, bytes := plan.PlannedCommands, plan.PlannedArgvBytes
	plan.PlannedCommands, plan.PlannedArgvBytes = 0, 0
	reply, refusal := m.Commit(plan)
	if refusal != nil || reply.Status != "ok" {
		t.Fatalf("Commit after public count edit: reply=%+v refusal=%v", reply, refusal)
	}
	plan.PlannedCommands, plan.PlannedArgvBytes = commands, bytes
	assertMemPlannedCounts(t, plan, reply)
	if reply.MemPlan == nil {
		t.Fatal("Commit lost captured normalized plan")
	}
	assertMemPlannedCounts(t, reply.MemPlan, reply)
	// The older single-call path reports the same planned counts as its reply.
	other := twoPhaseFixture(t)
	stepReply, err := other.Step(context.Background(), twoPhaseCreate())
	if err != nil || stepReply.MemPlan == nil {
		t.Fatalf("Step plan: reply=%+v err=%v", stepReply, err)
	}
	assertMemPlannedCounts(t, stepReply.MemPlan, stepReply)
}

func TestMemPlannedCountsEmptyRefusalFenceAndReplay(t *testing.T) {
	t.Parallel()
	m := twoPhaseFixture(t)
	empty := Step{Epoch: "0", Space: "two-phase:", Entries: []Entry{}}
	plan, refusal := m.Plan(empty)
	if refusal != nil || plan == nil {
		t.Fatalf("empty Plan: %+v refusal=%v", plan, refusal)
	}
	reply, refusal := m.Commit(plan)
	if refusal != nil || reply.Status != "ok" {
		t.Fatalf("empty Commit: reply=%+v refusal=%v", reply, refusal)
	}
	assertMemPlannedCounts(t, plan, reply)
	if plan.PlannedCommands != 0 || plan.PlannedArgvBytes != 0 {
		t.Fatalf("empty step planned work: %+v", plan)
	}
	before, err := m.Snapshot(empty.Space)
	if err != nil {
		t.Fatal(err)
	}
	bad := Step{Epoch: "0", Space: empty.Space, Entries: []Entry{{
		Kind: "create", Table: "work", To: "missing:c", IDs: []string{"bad"}, Scores: []string{"1"},
	}}}
	if failed, refusal := m.Plan(bad); failed != nil || refusal == nil {
		t.Fatalf("refused Plan returned counts: plan=%+v refusal=%v", failed, refusal)
	}
	after, err := m.Snapshot(empty.Space)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("refused Plan changed state: err=%v", err)
	}
	op, intent := "settle-op", "settle-intent"
	fence := Step{Epoch: "0", Space: empty.Space, Op: &op, Intent: &intent,
		Fence: true, Entries: []Entry{}}
	fencePlan, refusal := m.Plan(fence)
	if refusal != nil || fencePlan == nil || fencePlan.Replay ||
		fencePlan.PlannedCommands <= 0 || fencePlan.PlannedArgvBytes <= 0 {
		t.Fatalf("fence Plan must include receipt write: plan=%+v refusal=%v", fencePlan, refusal)
	}
	afterPlan, err := m.Snapshot(empty.Space)
	if err != nil || !reflect.DeepEqual(before, afterPlan) {
		t.Fatalf("fence Plan wrote receipt: err=%v", err)
	}
	fenceReply, refusal := m.Commit(fencePlan)
	if refusal != nil || fenceReply.Status != "fenced" || fenceReply.MemPlan != nil {
		t.Fatalf("fence Commit: reply=%+v refusal=%v", fenceReply, refusal)
	}
	assertMemPlannedCounts(t, fencePlan, fenceReply)
	replay, refusal := m.Plan(fence)
	if refusal != nil || replay == nil || !replay.Replay ||
		replay.PlannedCommands != 0 || replay.PlannedArgvBytes != 0 {
		t.Fatalf("replay Plan should carry no write: plan=%+v refusal=%v", replay, refusal)
	}
	replayed, refusal := m.Commit(replay)
	if refusal != nil || !replayed.Replay || replayed.MemPlan != nil {
		t.Fatalf("replay Commit: reply=%+v refusal=%v", replayed, refusal)
	}
}
