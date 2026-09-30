//go:build functional

package tset

import (
	"context"
	"reflect"
	"testing"
)

// These exact-name gates exercise the installed Lua writer and the independent
// Mem model on the same requests. Every step compares the complete semantic
// state, including historical epochs and receipts. Replay and refusal also
// compare the raw Redis key image, so an unmodeled write cannot hide in either.
type namedStateHarness struct {
	fx  *tsetFixture
	mem *Mem
}

func newNamedStateHarness(t *testing.T) *namedStateHarness {
	t.Helper()
	fx := newTSetFixture(t)
	fx.Define(t, "work", "c", "d")
	fx.Activate(t)
	mem := NewMem()
	if err := mem.DefineTable(fx.Space, "work", TableDefinition{
		Columns: []string{"c", "d"}, MemberPrefix: fx.Space + "member:work:",
		EpochKey: fx.Space + "sprint:epoch", EpochField: "n",
	}); err != nil {
		t.Fatalf("define model table: %v", err)
	}
	h := &namedStateHarness{fx: fx, mem: mem}
	h.snapshot(t)
	return h
}

func (h *namedStateHarness) snapshot(t *testing.T) MemSnapshot {
	t.Helper()
	model, err := h.mem.Snapshot(h.fx.Space)
	if err != nil {
		t.Fatalf("model snapshot: %v", err)
	}
	lua := h.fx.SemanticSnapshot(t)
	if !reflect.DeepEqual(model, lua) {
		t.Fatalf("Mem/Lua state differs: %s", compareJSON("state", model, lua))
	}
	return model
}

func (h *namedStateHarness) apply(t *testing.T, step Step) Reply {
	t.Helper()
	h.snapshot(t)
	model, modelErr := h.mem.Step(context.Background(), step)
	lua, luaErr := NewRedis(h.fx.Client).Step(context.Background(), step)
	if modelErr != nil || luaErr != nil {
		t.Fatalf("step refused unexpectedly: Mem=%v Lua=%v step=%+v", modelErr, luaErr, step)
	}
	model.Counters, lua.Counters = nil, nil
	if !reflect.DeepEqual(model, lua) {
		t.Fatalf("Mem/Lua replies differ: %s", compareJSON("reply", model, lua))
	}
	if model.Status != "ok" {
		t.Fatalf("step status=%q, want ok", model.Status)
	}
	h.snapshot(t)
	return model
}

func (h *namedStateHarness) unchanged(t *testing.T, step Step) Reply {
	t.Helper()
	before := h.snapshot(t)
	image := commitProbeImage(t, h.fx.Client)
	reply := h.apply(t, step)
	if after := h.snapshot(t); !reflect.DeepEqual(before, after) {
		t.Fatalf("step should not mutate semantic state: %s", compareJSON("state", before, after))
	}
	if after := commitProbeImage(t, h.fx.Client); !reflect.DeepEqual(image, after) {
		t.Fatal("step should not mutate Redis key image")
	}
	return reply
}

func (h *namedStateHarness) refuse(t *testing.T, step Step, code string) {
	t.Helper()
	before := h.snapshot(t)
	image := commitProbeImage(t, h.fx.Client)
	_, modelErr := h.mem.Step(context.Background(), step)
	_, luaErr := NewRedis(h.fx.Client).Step(context.Background(), step)
	model := requireRefusal(t, modelErr, code)
	lua := requireRefusal(t, luaErr, code)
	if !reflect.DeepEqual(comparableDetail(model.Detail), comparableDetail(lua.Detail)) {
		t.Fatalf("%s detail differs: Mem=%+v Lua=%+v", code, model.Detail, lua.Detail)
	}
	if after := h.snapshot(t); !reflect.DeepEqual(before, after) {
		t.Fatalf("%s refusal changed semantic state: %s", code, compareJSON("state", before, after))
	}
	if after := commitProbeImage(t, h.fx.Client); !reflect.DeepEqual(image, after) {
		t.Fatalf("%s refusal changed Redis key image", code)
	}
}

func stateStep(space string, epoch Decimal, entries ...Entry) Step {
	return Step{Space: space, Epoch: epoch, Entries: append([]Entry{}, entries...)}
}

func stateNamed(space string, epoch Decimal, op, intent, result string, entries ...Entry) Step {
	step := stateStep(space, epoch, entries...)
	step.Op, step.Intent, step.Result = &op, &intent, result
	return step
}

func stateWork(t *testing.T, snapshot MemSnapshot, epoch Decimal) MemTableSnapshot {
	t.Helper()
	e, found := snapshot.Epochs[epoch]
	if !found {
		t.Fatalf("epoch %q absent from snapshot", epoch)
	}
	table, found := e.Tables["work"]
	if !found {
		t.Fatalf("work table absent from epoch %q", epoch)
	}
	return table
}

func TestOnePlaceAndProspectiveRows(t *testing.T) {
	t.Parallel()
	h := newNamedStateHarness(t)
	space := h.fx.Space
	first := stateStep(space, "0",
		Entry{Kind: "rows", Table: "work", Add: []string{"old"}},
		Entry{Kind: "create", Table: "work", To: "old:c", IDs: []string{"p"}, Scores: []string{"1"}})
	if reply := h.apply(t, first); reply.Changed != 1 {
		t.Fatalf("prospective-row create changed=%d, want 1", reply.Changed)
	}
	move := stateStep(space, "0",
		Entry{Kind: "rows", Table: "work", Add: []string{"new"}, Del: []string{"old"}},
		Entry{Kind: "move", Table: "work", From: "old:c", To: "new:d", IDs: []string{"p"}, Revs: []Decimal{"1"}})
	if reply := h.apply(t, move); reply.Changed != 1 {
		t.Fatalf("move into prospective row changed=%d, want 1", reply.Changed)
	}
	table := stateWork(t, h.snapshot(t), "0")
	if _, old := table.Rows["old"]; old || table.Rows["new"] != "1" {
		t.Fatalf("final rows=%v, want only new at rank 1", table.Rows)
	}
	want := MemRecord{Epoch: "0", Revision: "2", Row: "new", Column: "d", Score: "1", Fields: map[string]string{}}
	if !reflect.DeepEqual(table.Records["p"], want) {
		t.Fatalf("member after move=%+v, want %+v", table.Records["p"], want)
	}
	placements := 0
	for _, columns := range table.Cells {
		for _, members := range columns {
			if _, exists := members["p"]; exists {
				placements++
			}
		}
	}
	if placements != 1 || table.Cells["new"]["d"]["p"] != "1" {
		t.Fatalf("member has %d placements, final cells=%v", placements, table.Cells)
	}
	h.refuse(t, stateStep(space, "0", Entry{Kind: "rows", Table: "work", Del: []string{"new"}}), "OCCUPIED")
}

func TestEffectiveDeltaRevision(t *testing.T) {
	t.Parallel()
	h := newNamedStateHarness(t)
	space := h.fx.Space
	h.apply(t, stateStep(space, "0", Entry{Kind: "rows", Table: "work", Add: []string{"r"}}))
	h.apply(t, stateStep(space, "0", Entry{Kind: "create", Table: "work", To: "r:c",
		IDs: []string{"p"}, Scores: []string{"1"}, Set: map[string]string{"empty": "", "state": "live"}}))
	noChange := stateStep(space, "0", Entry{Kind: "move", Table: "work", From: "r:c", To: "r:c",
		IDs: []string{"p"}, Scores: []string{"1.0"}, Revs: []Decimal{"1"},
		Set: map[string]string{"empty": "", "state": "live"}})
	if reply := h.unchanged(t, noChange); reply.Changed != 0 || !reflect.DeepEqual(reply.ChangedPerEntry, []int{0}) {
		t.Fatalf("same-cell effective no-op reply=%+v", reply)
	}
	absentUnset := stateStep(space, "0", Entry{Kind: "move", Table: "work", From: "r:c", To: "r:c",
		IDs: []string{"p"}, Revs: []Decimal{"1"}, Unset: []string{"absent"}})
	if reply := h.unchanged(t, absentUnset); reply.Changed != 0 {
		t.Fatalf("unset absent field changed=%d", reply.Changed)
	}
	removeEmpty := stateStep(space, "0", Entry{Kind: "move", Table: "work", From: "r:c", To: "r:c",
		IDs: []string{"p"}, Revs: []Decimal{"1"}, Unset: []string{"empty"}})
	if reply := h.apply(t, removeEmpty); reply.Changed != 1 {
		t.Fatalf("unset present empty field changed=%d, want 1", reply.Changed)
	}
	record := stateWork(t, h.snapshot(t), "0").Records["p"]
	if record.Revision != "2" || !reflect.DeepEqual(record.Fields, map[string]string{"state": "live"}) {
		t.Fatalf("effective field delta=%+v, want revision 2 and state only", record)
	}
	changeScore := stateStep(space, "0", Entry{Kind: "move", Table: "work", From: "r:c", To: "r:c",
		IDs: []string{"p"}, Revs: []Decimal{"2"}, Scores: []string{"2"}})
	if reply := h.apply(t, changeScore); reply.Changed != 1 {
		t.Fatalf("same-cell score change changed=%d, want 1", reply.Changed)
	}
	table := stateWork(t, h.snapshot(t), "0")
	if table.Records["p"].Revision != "3" || table.Cells["r"]["c"]["p"] != "2" {
		t.Fatalf("score delta revision/cell wrong: record=%+v cells=%v", table.Records["p"], table.Cells)
	}
}

func TestEpochAndAdvanceReplay(t *testing.T) {
	t.Parallel()
	h := newNamedStateHarness(t)
	space := h.fx.Space
	h.apply(t, stateStep(space, "0", Entry{Kind: "rows", Table: "work", Add: []string{"old"}},
		Entry{Kind: "create", Table: "work", To: "old:c", IDs: []string{"historical"}, Scores: []string{"1"}}))
	advance0 := stateNamed(space, "0", "advance-0", "fixed advance zero", "first clear",
		Entry{Kind: "advance", AdvanceFrom: "0"},
		Entry{Kind: "rows", Table: "work", Add: []string{"live"}},
		Entry{Kind: "create", Table: "work", To: "live:d", IDs: []string{"new"}, Scores: []string{"2"}})
	first := h.apply(t, advance0)
	if first.EpochBefore != "0" || first.EpochAfter != "1" || first.Replay {
		t.Fatalf("first advance reply=%+v", first)
	}
	snapshot := h.snapshot(t)
	if snapshot.ActiveEpoch != "1" || stateWork(t, snapshot, "0").Records["historical"].Row != "old" ||
		stateWork(t, snapshot, "1").Records["new"].Row != "live" {
		t.Fatalf("advance did not isolate old and new epochs: %+v", snapshot)
	}
	if _, copied := stateWork(t, snapshot, "1").Records["historical"]; copied {
		t.Fatal("advance copied an old-epoch member into the new epoch")
	}
	if _, copied := stateWork(t, snapshot, "1").Rows["old"]; copied {
		t.Fatal("advance copied an old-epoch row without restoration")
	}
	if _, saved := snapshot.Receipts["0"]["advance-0"]; !saved {
		t.Fatal("advance receipt missing from original epoch")
	}
	if _, duplicated := snapshot.Receipts["1"]["advance-0"]; duplicated {
		t.Fatal("advance receipt appeared in successor epoch")
	}
	advance1 := stateNamed(space, "1", "advance-1", "fixed advance one", "second clear",
		Entry{Kind: "advance", AdvanceFrom: "1"}, Entry{Kind: "rows", Table: "work", Add: []string{"restored"}})
	second := h.apply(t, advance1)
	if second.EpochAfter != "2" || h.snapshot(t).ActiveEpoch != "2" {
		t.Fatalf("second advance reply=%+v", second)
	}
	replay := h.unchanged(t, advance0)
	if !replay.Replay || replay.Result != "first clear" || replay.EpochBefore != "0" || replay.EpochAfter != "1" {
		t.Fatalf("old advance replay after two clears=%+v", replay)
	}
	h.refuse(t, stateStep(space, "0"), "STALE")
	h.refuse(t, stateStep(space, "3"), "EPOCHAHEAD")
}

func TestReplannedIntentReplayAcrossAdvance(t *testing.T) {
	t.Parallel()
	h := newNamedStateHarness(t)
	space := h.fx.Space
	h.apply(t, stateStep(space, "0", Entry{Kind: "rows", Table: "work", Add: []string{"r"}}))
	original := stateNamed(space, "0", "part-7", "stable semantic arguments", "recorded result",
		Entry{Kind: "create", Table: "work", To: "r:c", IDs: []string{"first"}, Scores: []string{"1"}})
	initial := h.apply(t, original)
	if initial.Replay || initial.Result != "recorded result" || initial.Changed != 1 {
		t.Fatalf("fresh named reply=%+v", initial)
	}
	replanned := stateNamed(space, "0", "part-7", "stable semantic arguments", "new estimate",
		Entry{Kind: "create", Table: "work", To: "r:d", IDs: []string{"should-not-exist"}, Scores: []string{"5"}})
	firstReplay := h.unchanged(t, replanned)
	if !firstReplay.Replay || firstReplay.Result != "recorded result" || firstReplay.Changed != 1 {
		t.Fatalf("same-epoch replan did not replay original result: %+v", firstReplay)
	}
	h.apply(t, stateNamed(space, "0", "clear-0", "clear intent", "",
		Entry{Kind: "advance", AdvanceFrom: "0"}, Entry{Kind: "rows", Table: "work", Add: []string{"r"}}))
	staleReplay := h.unchanged(t, replanned)
	if !staleReplay.Replay || staleReplay.Result != "recorded result" || staleReplay.EpochBefore != "0" {
		t.Fatalf("replan after advance did not replay original result: %+v", staleReplay)
	}
	if _, exists := stateWork(t, h.snapshot(t), "0").Records["should-not-exist"]; exists {
		t.Fatal("replanned member was committed during replay")
	}
	changedIntent := replanned
	other := "changed semantic arguments"
	changedIntent.Intent = &other
	h.refuse(t, changedIntent, "OPCONFLICT")
}

func TestUnrelatedBeatDoesNotConflict(t *testing.T) {
	t.Parallel()
	h := newNamedStateHarness(t)
	space := h.fx.Space
	h.apply(t, stateStep(space, "0", Entry{Kind: "rows", Table: "work", Add: []string{"r"}},
		Entry{Kind: "create", Table: "work", To: "r:c", IDs: []string{"a", "b"}, Scores: []string{"1", "2"}}))
	// Form b's guarded beat while both members are at revision 1. Updating a
	// must not invalidate this already planned, unrelated member guard.
	beatB := stateStep(space, "0", Entry{Kind: "move", Table: "work", From: "r:c", To: "r:c",
		IDs: []string{"b"}, Revs: []Decimal{"1"}, Set: map[string]string{"beat": "b1"}})
	beatA := stateStep(space, "0", Entry{Kind: "move", Table: "work", From: "r:c", To: "r:c",
		IDs: []string{"a"}, Revs: []Decimal{"1"}, Set: map[string]string{"beat": "a1"}})
	if reply := h.apply(t, beatA); reply.Changed != 1 {
		t.Fatalf("a beat changed=%d, want 1", reply.Changed)
	}
	if reply := h.apply(t, beatB); reply.Changed != 1 {
		t.Fatalf("unrelated b beat changed=%d, want 1", reply.Changed)
	}
	table := stateWork(t, h.snapshot(t), "0")
	for id, want := range map[string]string{"a": "a1", "b": "b1"} {
		record := table.Records[id]
		if record.Revision != "2" || record.Fields["beat"] != want {
			t.Fatalf("%s after independent beat=%+v, want revision 2 and beat %q", id, record, want)
		}
	}
	h.refuse(t, beatA, "REVISION")
}
