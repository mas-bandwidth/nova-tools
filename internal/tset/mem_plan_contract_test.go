package tset

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func memPlanFixture(t *testing.T) *Mem {
	t.Helper()
	const space = "mem-plan:"
	m := NewMem()
	if err := m.DefineTable(space, "work", TableDefinition{Columns: []string{"c", "d"},
		MemberPrefix: space + "member:work:", EpochKey: space + "sprint:epoch", EpochField: "n"}); err != nil {
		t.Fatal(err)
	}
	if err := m.SeedRow(space, "work", "0", "r0", "0"); err != nil {
		t.Fatal(err)
	}
	return m
}

func memPlanStep(t *testing.T, m *Mem, step Step) Reply {
	t.Helper()
	reply, err := m.Step(context.Background(), step)
	if err != nil {
		t.Fatalf("step %+v: %v", step, err)
	}
	return reply
}

func TestMemPlanProjectsActualMemberEffectsAndCopies(t *testing.T) {
	t.Parallel()
	const space = "mem-plan:"
	m := memPlanFixture(t)
	create := Step{Epoch: "0", Space: space, Entries: []Entry{
		{Kind: "rows", Table: "work", Add: []string{"r1", "r1"}},
		{Kind: "create", Table: "work", To: "r1:c", IDs: []string{"a"}, Scores: []string{"1.0"},
			Set:          map[string]string{"status": "new", "secret": "hidden"},
			BeforeFields: []string{"absent"}, About: []string{"primary-a"}, Meta: json.RawMessage(`{"tag":"create"}`)},
	}}
	r := memPlanStep(t, m, create)
	if r.MemPlan == nil || len(r.MemPlan.Entries) != 2 || len(r.MemPlan.Entries[0].Added) != 1 ||
		r.MemPlan.Entries[0].Added[0] != (RowRank{Row: "r1", Rank: "1"}) ||
		!reflect.DeepEqual(r.MemPlan.Entries[0].Entry.Add, []string{"r1"}) {
		t.Fatalf("rows plan: %+v", r.MemPlan)
	}
	created := r.MemPlan.Entries[1]
	if created.Index != 1 || !reflect.DeepEqual(created.Entry.IDs, []string{"a"}) ||
		!reflect.DeepEqual(created.Entry.Scores, []string{"1.0"}) ||
		len(created.Before) != 1 || created.Before[0].Exists || len(created.After) != 1 ||
		created.After[0].Revision != "1" || created.After[0].Score != "1" ||
		created.After[0].Fields["absent"].Present ||
		!reflect.DeepEqual(created.FieldChanges[0].Set, map[string]string{"status": "new", "secret": "hidden"}) {
		t.Fatalf("create plan: %+v", created)
	}
	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if _, leaked := wire["MemPlan"]; leaked {
		t.Fatalf("Mem plan leaked into reply JSON: %s", encoded)
	}
	if _, leaked := wire["mem_plan"]; leaked {
		t.Fatalf("Mem plan leaked into reply JSON: %s", encoded)
	}
	decoded, err := DecodeReply(encoded)
	if err != nil || decoded.MemPlan != nil {
		t.Fatalf("wire decoder gained Mem plan: reply=%+v err=%v", decoded, err)
	}
	// Changing the returned plan or the caller's input cannot alter the model.
	create.Entries[1].Set["secret"] = "input changed"
	r.MemPlan.Entries[1].After[0].Fields["secret"] = FieldValue{Present: true, Value: "reply changed"}
	r.MemPlan.Entries[1].FieldChanges[0].Set["secret"] = "reply changed"
	r.MemPlan.Entries[1].Entry.Meta[0] = 'x'
	snapshot, err := m.Snapshot(space)
	if err != nil || snapshot.Epochs["0"].Tables["work"].Records["a"].Fields["secret"] != "hidden" {
		t.Fatalf("returned plan aliases state: snapshot=%+v err=%v", snapshot, err)
	}

	move := Step{Epoch: "0", Space: space, Entries: []Entry{{Kind: "move", Table: "work", From: "r1:c",
		IDs: []string{"a"}, Scores: []string{"1.00"}, Set: map[string]string{"status": "ready"},
		BeforeFields: []string{"absent"}}}}
	movedReply := memPlanStep(t, m, move)
	moved := movedReply.MemPlan.Entries[0]
	if !reflect.DeepEqual(moved.Entry.IDs, []string{"a"}) || moved.Before[0].Revision != "1" ||
		moved.After[0].Revision != "2" || moved.After[0].Score != "1" ||
		!reflect.DeepEqual(moved.Entry.Scores, []string{"1.00"}) ||
		len(moved.Before[0].Fields) != 2 || moved.Before[0].Fields["secret"].Present ||
		moved.Before[0].Fields["status"].Value != "new" ||
		!reflect.DeepEqual(moved.FieldChanges[0].Set, map[string]string{"status": "ready"}) {
		t.Fatalf("projected field-only move: %+v", moved)
	}
	if movedReply.MemPlan.Before["work"]["a"].Fields["status"].Value != "new" {
		t.Fatalf("changed-member before observation missing: %+v", movedReply.MemPlan.Before)
	}
	observed := movedReply.MemPlan.Before["work"]["a"]
	observed.Fields["status"] = FieldValue{Present: true, Value: "mutated"}
	observed.Place.Row = "mutated"
	if moved.Before[0].Fields["status"].Value != "new" || moved.Before[0].Place.Row != "r1" {
		t.Fatalf("plan-level before aliases aligned entry before: %+v", moved)
	}
	guard := memPlanStep(t, m, Step{Epoch: "0", Space: space, Entries: []Entry{{Kind: "guard",
		Table: "work", From: "r1:c", IDs: []string{"a"}, BeforeFields: []string{"status", "secret", "absent"}}}})
	guardBefore := guard.MemPlan.Before["work"]["a"]
	if guard.MemPlan == nil || len(guard.MemPlan.Entries) != 1 ||
		len(guard.MemPlan.Entries[0].Entry.IDs) != 0 || len(guard.MemPlan.Entries[0].Before) != 0 ||
		guard.Changed != 0 || guard.Guarded != 1 || len(guardBefore.Fields) != 3 ||
		guardBefore.Fields["status"].Value != "ready" || guardBefore.Fields["secret"].Value != "hidden" ||
		guardBefore.Fields["absent"].Present {
		t.Fatalf("guard slot/observation: %+v", guard)
	}
	guardBefore.Fields["status"] = FieldValue{Present: true, Value: "mutated"}
	noop := memPlanStep(t, m, Step{Epoch: "0", Space: space, Entries: []Entry{{Kind: "move",
		Table: "work", From: "r1:c", IDs: []string{"a"}, Set: map[string]string{"status": "ready"},
		BeforeFields: []string{"absent"}}}})
	if noop.MemPlan == nil || len(noop.MemPlan.Entries) != 1 ||
		len(noop.MemPlan.Entries[0].Entry.IDs) != 0 || len(noop.MemPlan.Entries[0].Before) != 0 ||
		noop.Changed != 0 || noop.MemPlan.Before["work"]["a"].Fields["status"].Value != "ready" ||
		noop.MemPlan.Before["work"]["a"].Fields["absent"].Present {
		t.Fatalf("no-op slot/observation: %+v", noop)
	}
	removed := memPlanStep(t, m, Step{Epoch: "0", Space: space, Entries: []Entry{{Kind: "remove", Table: "work",
		From: "r1:c", IDs: []string{"a"}, Unset: []string{"status"}}}}).MemPlan.Entries[0]
	if len(removed.After) != 1 || !removed.After[0].Exists || removed.After[0].Place != nil ||
		removed.After[0].Revision != "3" || len(removed.FieldChanges[0].Unset) != 1 ||
		removed.FieldChanges[0].Unset[0] != "status" || removed.After[0].Fields["secret"].Present {
		t.Fatalf("remove plan: %+v", removed)
	}
}

func TestMemPlanAdvanceReplayFenceAndRefusal(t *testing.T) {
	t.Parallel()
	const space = "mem-plan:"
	m := memPlanFixture(t)
	op, intent := "advance-op", "advance-intent"
	step := Step{Epoch: "0", Space: space, Op: &op, Intent: &intent, Entries: []Entry{
		{Kind: "rowset", Table: "work", Rows: []RowRank{{Row: "r0", Rank: "0"}}},
		{Kind: "advance", AdvanceFrom: "0"},
		{Kind: "rows", Table: "work", Add: []string{"r1"}},
		{Kind: "create", Table: "work", To: "r1:d", IDs: []string{"b"}, Scores: []string{"2"}},
	}}
	fresh := memPlanStep(t, m, step)
	if fresh.MemPlan == nil || len(fresh.MemPlan.Entries) != 4 || fresh.EpochAfter != "1" ||
		len(fresh.MemPlan.Entries[0].Entry.Rows) != 1 ||
		fresh.MemPlan.Entries[1].Entry.AdvanceFrom != "0" || fresh.MemPlan.Entries[1].Entry.Each != nil ||
		len(fresh.MemPlan.Entries[2].Added) != 1 || fresh.MemPlan.Entries[2].Added[0].Rank != "0" ||
		fresh.MemPlan.Entries[3].After[0].Epoch != "1" {
		t.Fatalf("advance plan: %+v", fresh)
	}
	replay := memPlanStep(t, m, step)
	if !replay.Replay || replay.MemPlan != nil {
		t.Fatalf("replay exposed plan: %+v", replay)
	}
	fence := Step{Epoch: "1", Space: space, Fence: true, Entries: []Entry{}}
	fenceOp, fenceIntent := "fence-op", "fence-intent"
	fence.Op, fence.Intent = &fenceOp, &fenceIntent
	if reply := memPlanStep(t, m, fence); reply.Status != "fenced" || reply.MemPlan != nil {
		t.Fatalf("fence exposed plan: %+v", reply)
	}
	before, err := m.Snapshot(space)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := m.Step(context.Background(), Step{Epoch: "1", Space: space, Entries: []Entry{
		{Kind: "create", Table: "work", To: "missing:c", IDs: []string{"c"}, Scores: []string{"1"}},
	}})
	if err == nil || failed.MemPlan != nil {
		t.Fatalf("refusal exposed plan: reply=%+v err=%v", failed, err)
	}
	after, err := m.Snapshot(space)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("refusal mutated state: err=%v", err)
	}
	empty := memPlanStep(t, m, Step{Epoch: "1", Space: space, Entries: []Entry{}})
	if empty.MemPlan == nil || len(empty.MemPlan.Entries) != 0 {
		t.Fatalf("ordinary empty step omitted plan: %+v", empty)
	}
}

func TestMemPlanFiltersAlignedArraysAndTopologyNoops(t *testing.T) {
	t.Parallel()
	const space = "mem-plan:"
	m := memPlanFixture(t)
	memPlanStep(t, m, Step{Epoch: "0", Space: space, Entries: []Entry{{Kind: "create", Table: "work",
		To: "r0:c", IDs: []string{"a", "b"}, Scores: []string{"1", "2"}, Set: map[string]string{"status": "ready"}}}})
	r := memPlanStep(t, m, Step{Epoch: "0", Space: space, Entries: []Entry{
		{Kind: "rows", Table: "work", Add: []string{"r0"}}, // existing row: no topology effect
		{Kind: "move", Table: "work", From: "r0:c", IDs: []string{"a", "b"},
			Scores: []string{"1.0", "2"}, Revs: []Decimal{"1", "1"}, About: []string{"pa", "pb"},
			Each: []map[string]string{{"status": "next"}, {}}, BeforeFields: []string{"status"}},
	}})
	if r.MemPlan == nil || len(r.MemPlan.Entries) != 2 ||
		len(r.MemPlan.Entries[0].Added) != 0 || len(r.MemPlan.Entries[0].Entry.Add) != 0 {
		t.Fatalf("topology no-op slot: %+v", r)
	}
	member := r.MemPlan.Entries[1]
	if r.Changed != 1 || !reflect.DeepEqual(member.Entry.IDs, []string{"a"}) ||
		!reflect.DeepEqual(member.Entry.Scores, []string{"1.0"}) ||
		!reflect.DeepEqual(member.Entry.Revs, []Decimal{"1"}) ||
		!reflect.DeepEqual(member.Entry.About, []string{"pa"}) ||
		len(member.Before) != 1 || len(member.After) != 1 || len(member.FieldChanges) != 1 ||
		!reflect.DeepEqual(member.FieldChanges[0].Set, map[string]string{"status": "next"}) ||
		r.MemPlan.Before["work"]["b"].Fields["status"].Value != "ready" ||
		len(r.MemPlan.Before["work"]["b"].Fields) != 1 {
		t.Fatalf("aligned changed-only plan: %+v", member)
	}
	// Delete a different empty row to check that topology removals are explicit.
	memPlanStep(t, m, Step{Epoch: "0", Space: space, Entries: []Entry{{Kind: "rows", Table: "work", Add: []string{"empty"}}}})
	deleted := memPlanStep(t, m, Step{Epoch: "0", Space: space, Entries: []Entry{
		{Kind: "rows", Table: "work", Del: []string{"empty"}},
	}}).MemPlan.Entries[0]
	if !reflect.DeepEqual(deleted.Entry.Del, []string{"empty"}) ||
		!reflect.DeepEqual(deleted.Deleted, []string{"empty"}) {
		t.Fatalf("deleted topology plan: %+v", deleted)
	}
}
