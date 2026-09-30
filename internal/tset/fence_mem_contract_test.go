package tset

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func fenceMemFixture(t *testing.T) *Mem {
	t.Helper()
	const space = "fence-mem:"
	m := NewMem()
	if err := m.DefineTable(space, "work", TableDefinition{
		Columns: []string{"c"}, MemberPrefix: space + "member:work:",
		EpochKey: space + "sprint:epoch", EpochField: "n",
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.SeedRow(space, "work", "0", "r", "0"); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMemFenceWinsReceiptAndBlocksLateOriginal(t *testing.T) {
	const space = "fence-mem:"
	m := fenceMemFixture(t)
	op, intent := "part-1", "stable logical part"
	fence := Step{Epoch: "0", Space: space, Op: &op, Intent: &intent, Fence: true, Entries: []Entry{}}
	before, err := m.Snapshot(space)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := m.Step(context.Background(), fence)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Status != "fenced" || fresh.EpochBefore != "0" || fresh.EpochAfter != "0" ||
		fresh.Changed != 0 || fresh.Guarded != 0 || len(fresh.ChangedPerEntry) != 0 ||
		fresh.FirstSeq != "0" || fresh.LastSeq != "0" || fresh.Lines != 0 ||
		fresh.Result != "" || fresh.Replay {
		t.Fatalf("fresh fence envelope: %+v", fresh)
	}
	var work struct {
		PlannedCommands int `json:"planned_commands"`
		PlannedBytes    int `json:"planned_argv_bytes"`
		Candidates      int `json:"candidates"`
		CellProbes      int `json:"cell_probes"`
	}
	if err := json.Unmarshal(fresh.Counters, &work); err != nil {
		t.Fatal(err)
	}
	stored := encodeMemReceipt(memReceiptForReply(fence, fresh))
	wantBytes := len("HSET") + len(space+"sprint:done@0") + len(op) + len(stored)
	if work.PlannedCommands != 1 || work.PlannedBytes != wantBytes || work.Candidates != 0 || work.CellProbes != 0 {
		t.Fatalf("fence receipt work: %+v, want one HSET and %d bytes", work, wantBytes)
	}
	after, err := m.Snapshot(space)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after.Epochs, before.Epochs) || after.ActiveEpoch != before.ActiveEpoch ||
		!reflect.DeepEqual(after.Definitions, before.Definitions) || !reflect.DeepEqual(after.ZSets, before.ZSets) ||
		after.Receipts["0"][op].Status != "fenced" {
		t.Fatalf("fence changed table state or missed receipt: before=%+v after=%+v", before, after)
	}
	original := Step{Epoch: "0", Space: space, Op: &op, Intent: &intent, Result: "applied",
		Entries: []Entry{{Kind: "create", Table: "work", To: "r:c", IDs: []string{"new"}, Scores: []string{"1"}}}}
	replay, err := m.Step(context.Background(), original)
	if err != nil || replay.Status != "fenced" || !replay.Replay || replay.Result != "" || replay.Changed != 0 {
		t.Fatalf("late original was not fenced: reply=%+v err=%v", replay, err)
	}
	late, err := m.Snapshot(space)
	if err != nil || !reflect.DeepEqual(late, after) {
		t.Fatalf("late original changed state: err=%v", err)
	}
	read, err := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: space,
		Queries: []ReadQuery{{Kind: "done", Ops: []DoneIdentity{{Epoch: "0", Op: op, IntentDigest: intentDigest(intent)}}}}})
	if err != nil || len(read.Answers) != 1 || len(read.Answers[0].Done) != 1 ||
		read.Answers[0].Done[0].Receipt == nil || read.Answers[0].Done[0].Receipt.Status != "fenced" {
		t.Fatalf("done did not retain fenced status: reply=%+v err=%v", read, err)
	}
}

func TestMemOriginalWinsThenFenceReplaysOK(t *testing.T) {
	const space = "fence-mem:"
	m := fenceMemFixture(t)
	op, intent := "part-2", "stable logical part"
	original := Step{Epoch: "0", Space: space, Op: &op, Intent: &intent, Result: "applied",
		Entries: []Entry{{Kind: "create", Table: "work", To: "r:c", IDs: []string{"new"}, Scores: []string{"1"}}}}
	fresh, err := m.Step(context.Background(), original)
	if err != nil || fresh.Status != "ok" || fresh.Changed != 1 {
		t.Fatalf("original did not apply: reply=%+v err=%v", fresh, err)
	}
	before, err := m.Snapshot(space)
	if err != nil {
		t.Fatal(err)
	}
	fence := Step{Epoch: "0", Space: space, Op: &op, Intent: &intent, Fence: true, Entries: []Entry{}}
	replay, err := m.Step(context.Background(), fence)
	if err != nil || replay.Status != "ok" || !replay.Replay || replay.Result != "applied" || replay.Changed != 1 {
		t.Fatalf("fence did not replay winning original: reply=%+v err=%v", replay, err)
	}
	after, err := m.Snapshot(space)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("losing fence changed state: err=%v", err)
	}
}

func TestMemNamedEmptyStepIsNotFence(t *testing.T) {
	const space = "fence-mem:"
	m := fenceMemFixture(t)
	op, intent := "empty-part", "empty but ordinary"
	fresh, err := m.Step(context.Background(), Step{Epoch: "0", Space: space, Op: &op, Intent: &intent, Entries: []Entry{}})
	if err != nil || fresh.Status != "ok" || fresh.Replay || fresh.Changed != 0 {
		t.Fatalf("ordinary empty step was fenced: reply=%+v err=%v", fresh, err)
	}
	fence := Step{Epoch: "0", Space: space, Op: &op, Intent: &intent, Fence: true, Entries: []Entry{}}
	replay, err := m.Step(context.Background(), fence)
	if err != nil || replay.Status != "ok" || !replay.Replay {
		t.Fatalf("fence failed to replay ordinary empty receipt: reply=%+v err=%v", replay, err)
	}
}
