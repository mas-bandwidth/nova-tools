package tset

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// stateWitnessRecord projects a public snapshot, rather than the planner's
// private record, so every plan record is checked against committed state.
func stateWitnessRecord(snapshot MemSnapshot, epoch Decimal, table, id string, fields []string) MemberRecord {
	want := MemberRecord{ID: id, Fields: make(map[string]FieldValue, len(fields))}
	record, exists := snapshot.Epochs[epoch].Tables[table].Records[id]
	if exists {
		want.Exists = true
		want.Epoch = record.Epoch
		want.Revision = record.Revision
		want.Score = record.Score
		if record.Row != "" {
			want.Place = &CellPlace{Row: record.Row, Col: record.Column}
		}
	}
	for _, field := range fields {
		value, present := record.Fields[field]
		want.Fields[field] = FieldValue{Present: present, Value: value}
	}
	return want
}

func stateWitnessRows(snapshot MemSnapshot, epoch Decimal, table string) map[string]Decimal {
	return snapshot.Epochs[epoch].Tables[table].Rows
}

func stateWitnessStep(t *testing.T, m *Mem, step Step, changed [][]string,
	fields map[string][]string, rowEffects map[int]struct {
		added   []RowRank
		deleted []string
	}) (MemSnapshot, MemSnapshot, Reply) {
	t.Helper()
	before, err := m.Snapshot(step.Space)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := m.Step(context.Background(), step)
	if err != nil || reply.Status != "ok" || reply.MemPlan == nil {
		t.Fatalf("Step reply=%+v err=%v", reply, err)
	}
	after, err := m.Snapshot(step.Space)
	if err != nil {
		t.Fatal(err)
	}
	plan := reply.MemPlan
	if len(plan.Entries) != len(step.Entries) || len(changed) != len(step.Entries) {
		t.Fatalf("combined Step/plan slot count: step=%d plan=%d expected=%d", len(step.Entries), len(plan.Entries), len(changed))
	}
	observed := make(map[string]map[string]bool)
	rowTables := make(map[string]bool)
	rowAdds := make(map[string]map[string]Decimal)
	rowDels := make(map[string]map[string]bool)
	for i, request := range step.Entries {
		got := plan.Entries[i]
		if got.Index != i || got.Entry.Kind != request.Kind || got.Entry.Table != request.Table ||
			!reflect.DeepEqual(got.Entry.IDs, changed[i]) || len(got.Before) != len(changed[i]) ||
			len(got.After) != len(changed[i]) || len(got.FieldChanges) != len(changed[i]) {
			t.Fatalf("entry %d lost combined order or changed-ID alignment: request=%+v plan=%+v", i, request, got)
		}
		for j, id := range got.Entry.IDs {
			projection, ok := fields[id]
			if !ok {
				t.Fatalf("entry %d planned unexpected ID %q", i, id)
			}
			wantBefore := stateWitnessRecord(before, reply.EpochAfter, request.Table, id, projection)
			wantAfter := stateWitnessRecord(after, reply.EpochAfter, request.Table, id, projection)
			if !reflect.DeepEqual(got.Before[j], wantBefore) || !reflect.DeepEqual(got.After[j], wantAfter) {
				t.Fatalf("entry %d ID %q projected snapshot mismatch: before=%+v want=%+v after=%+v want=%+v",
					i, id, got.Before[j], wantBefore, got.After[j], wantAfter)
			}
			wantSet := make(map[string]string)
			wantUnset := make(map[string]bool)
			for _, field := range projection {
				prior, next := wantBefore.Fields[field], wantAfter.Fields[field]
				if next.Present && (!prior.Present || prior.Value != next.Value) {
					wantSet[field] = next.Value
				} else if prior.Present && !next.Present {
					wantUnset[field] = true
				}
			}
			gotUnset := make(map[string]bool)
			for _, field := range got.FieldChanges[j].Unset {
				gotUnset[field] = true
			}
			if !reflect.DeepEqual(got.FieldChanges[j].Set, wantSet) || !reflect.DeepEqual(gotUnset, wantUnset) ||
				len(got.FieldChanges[j].Unset) != len(wantUnset) {
				t.Fatalf("entry %d ID %q effective field delta=%+v; snapshot set=%v unset=%v",
					i, id, got.FieldChanges[j], wantSet, wantUnset)
			}
		}
		if request.Kind == "create" || request.Kind == "move" || request.Kind == "remove" || request.Kind == "guard" {
			if observed[request.Table] == nil {
				observed[request.Table] = make(map[string]bool)
			}
			for _, id := range request.IDs {
				observed[request.Table][id] = true
				projection, ok := fields[id]
				if !ok {
					t.Fatalf("entry %d lacks expected projection for observed ID %q", i, id)
				}
				want := stateWitnessRecord(before, reply.EpochAfter, request.Table, id, projection)
				if !reflect.DeepEqual(plan.Before[request.Table][id], want) {
					t.Fatalf("entry %d observed ID %q before=%+v; snapshot=%+v", i, id, plan.Before[request.Table][id], want)
				}
			}
		}
		switch request.Kind {
		case "rows":
			rowTables[request.Table] = true
			want := rowEffects[i]
			if !reflect.DeepEqual(got.Added, want.added) || !reflect.DeepEqual(got.Deleted, want.deleted) ||
				len(got.Entry.Add) != len(got.Added) || len(got.Entry.Del) != len(got.Deleted) {
				t.Fatalf("entry %d topology effect=%+v; want added=%v deleted=%v", i, got, want.added, want.deleted)
			}
			for j, addition := range got.Added {
				if got.Entry.Add[j] != addition.Row || stateWitnessRows(after, reply.EpochAfter, request.Table)[addition.Row] != addition.Rank {
					t.Fatalf("entry %d added row/rank absent from post-state: %+v", i, addition)
				}
				if rowAdds[request.Table] == nil {
					rowAdds[request.Table] = make(map[string]Decimal)
				}
				rowAdds[request.Table][addition.Row] = addition.Rank
			}
			for j, deletion := range got.Deleted {
				if got.Entry.Del[j] != deletion {
					t.Fatalf("entry %d deleted row mismatch: %q", i, deletion)
				}
				if rowDels[request.Table] == nil {
					rowDels[request.Table] = make(map[string]bool)
				}
				rowDels[request.Table][deletion] = true
			}
		case "rowset":
			rows := stateWitnessRows(before, step.Epoch, request.Table)
			if !reflect.DeepEqual(got.Entry.Rows, request.Rows) || len(got.Entry.Rows) != len(rows) {
				t.Fatalf("rowset does not cover request-epoch rows: plan=%+v rows=%v", got.Entry.Rows, rows)
			}
			for _, named := range got.Entry.Rows {
				if rows[named.Row] != named.Rank {
					t.Fatalf("rowset rank differs from request snapshot: %+v rows=%v", named, rows)
				}
			}
		case "advance":
			if got.Entry.AdvanceFrom != step.Epoch || before.ActiveEpoch != step.Epoch ||
				after.ActiveEpoch != reply.EpochAfter || reply.EpochAfter == step.Epoch ||
				!reflect.DeepEqual(before.Epochs[step.Epoch], after.Epochs[step.Epoch]) {
				t.Fatalf("advance slot or retained source epoch differs: entry=%+v before=%+v after=%+v", got, before, after)
			}
		}
	}
	if len(plan.Before) != len(observed) {
		t.Fatalf("plan before table set=%v; observed=%v", plan.Before, observed)
	}
	for table, ids := range observed {
		if len(plan.Before[table]) != len(ids) {
			t.Fatalf("plan before IDs for %s=%v; observed=%v", table, plan.Before[table], ids)
		}
	}
	for table := range rowTables {
		prior, next := stateWitnessRows(before, reply.EpochAfter, table), stateWitnessRows(after, reply.EpochAfter, table)
		actualAdds, actualDels := make(map[string]Decimal), make(map[string]bool)
		for row, rank := range next {
			if _, existed := prior[row]; !existed {
				actualAdds[row] = rank
			}
		}
		for row := range prior {
			if _, exists := next[row]; !exists {
				actualDels[row] = true
			}
		}
		if len(actualAdds) != len(rowAdds[table]) || len(actualDels) != len(rowDels[table]) ||
			(len(actualAdds) != 0 && !reflect.DeepEqual(actualAdds, rowAdds[table])) ||
			(len(actualDels) != 0 && !reflect.DeepEqual(actualDels, rowDels[table])) {
			t.Fatalf("table %s plan row delta add=%v del=%v; snapshot add=%v del=%v",
				table, rowAdds[table], rowDels[table], actualAdds, actualDels)
		}
	}
	return before, after, reply
}

func TestMemStepExposesNormalizedPlan(t *testing.T) {
	t.Parallel()
	const space = "plan-state:"
	m := NewMem()
	if err := m.DefineTable(space, "work", TableDefinition{Columns: []string{"c", "d"},
		MemberPrefix: space + "member:work:", EpochKey: space + "sprint:epoch", EpochField: "n"}); err != nil {
		t.Fatal(err)
	}
	if err := m.SeedRow(space, "work", "0", "r0", "0"); err != nil {
		t.Fatal(err)
	}
	type rows = struct {
		added   []RowRank
		deleted []string
	}
	create := Step{Epoch: "0", Space: space, Entries: []Entry{
		{Kind: "rows", Table: "work", Add: []string{"r1"}},
		{Kind: "create", Table: "work", To: "r1:c", IDs: []string{"a", "b", "c"},
			Scores: []string{"1.0", "2", "3"}, Set: map[string]string{"status": "new"},
			Each:         []map[string]string{{"owner": "A"}, {"owner": "B"}, {"owner": "C"}},
			BeforeFields: []string{"missing"}, About: []string{"pa", "pb", "pc"}},
	}}
	_, _, created := stateWitnessStep(t, m, create, [][]string{nil, {"a", "b", "c"}},
		map[string][]string{"a": {"status", "owner", "missing"}, "b": {"status", "owner", "missing"}, "c": {"status", "owner", "missing"}},
		map[int]rows{0: {added: []RowRank{{Row: "r1", Rank: "1"}}}})
	if !reflect.DeepEqual(created.MemPlan.Entries[1].Entry.Scores, []string{"1.0", "2", "3"}) ||
		!reflect.DeepEqual(created.MemPlan.Entries[1].Entry.About, []string{"pa", "pb", "pc"}) {
		t.Fatalf("create aligned score/about arrays: %+v", created.MemPlan.Entries[1])
	}

	move := Step{Epoch: "0", Space: space, Entries: []Entry{
		{Kind: "rows", Table: "work", Add: []string{"r2"}},
		{Kind: "rows", Table: "work", Add: []string{"r0"}}, // effective no-op slot
		{Kind: "move", Table: "work", From: "r1:c", To: "r2:d", IDs: []string{"a"},
			Scores: []string{"1.00"}, Set: map[string]string{"status": "ready"},
			BeforeFields: []string{"owner", "missing"}, About: []string{"pa"}, Meta: json.RawMessage(`{"why":"move"}`)},
		{Kind: "guard", Table: "work", From: "r1:c", IDs: []string{"b"}, BeforeFields: []string{"owner", "missing"}},
		{Kind: "move", Table: "work", From: "r1:c", IDs: []string{"c"},
			Set: map[string]string{"status": "new"}, BeforeFields: []string{"owner"}}, // member no-op slot
	}}
	_, movedState, moved := stateWitnessStep(t, m, move, [][]string{nil, nil, {"a"}, nil, nil},
		map[string][]string{"a": {"status", "owner", "missing"}, "b": {"owner", "missing"}, "c": {"status", "owner"}},
		map[int]rows{0: {added: []RowRank{{Row: "r2", Rank: "2"}}}, 1: {}})
	if moved.Changed != 1 || moved.Guarded != 1 ||
		!reflect.DeepEqual(moved.ChangedPerEntry, []int{0, 0, 1, 0, 0}) ||
		!reflect.DeepEqual(moved.MemPlan.Entries[2].Entry.Scores, []string{"1.00"}) ||
		len(moved.MemPlan.Entries[3].Entry.IDs) != 0 || len(moved.MemPlan.Entries[4].Entry.IDs) != 0 {
		t.Fatalf("combined changed/guard/no-op alignment: %+v", moved)
	}
	move.Entries[2].Scores[0] = "9"
	move.Entries[2].Meta[0] = 'x'
	if !reflect.DeepEqual(moved.MemPlan.Entries[2].Entry.Scores, []string{"1.00"}) ||
		string(moved.MemPlan.Entries[2].Entry.Meta) != `{"why":"move"}` {
		t.Fatalf("returned plan aliases request: %+v", moved.MemPlan.Entries[2])
	}
	moved.MemPlan.Before["work"]["a"] = MemberRecord{ID: "mutated"}
	moved.MemPlan.Entries[2].Before[0].Place.Row = "mutated"
	moved.MemPlan.Entries[2].After[0].Fields["status"] = FieldValue{Present: true, Value: "mutated"}
	moved.MemPlan.Entries[2].FieldChanges[0].Set["status"] = "mutated"
	moved.MemPlan.Entries[0].Added[0].Rank = "999"
	stillMoved, err := m.Snapshot(space)
	if err != nil || !reflect.DeepEqual(stillMoved, movedState) {
		t.Fatalf("mutating returned plan changed Mem: snapshot=%+v err=%v", stillMoved, err)
	}

	remove := Step{Epoch: "0", Space: space, Entries: []Entry{
		{Kind: "rows", Table: "work", Del: []string{"r1"}}, // applied after both removals
		{Kind: "remove", Table: "work", From: "r1:c", IDs: []string{"b", "c"},
			Unset: []string{"status"}, BeforeFields: []string{"owner", "missing"}},
	}}
	_, removedState, removed := stateWitnessStep(t, m, remove, [][]string{nil, {"b", "c"}},
		map[string][]string{"b": {"status", "owner", "missing"}, "c": {"status", "owner", "missing"}},
		map[int]rows{0: {deleted: []string{"r1"}}})
	if removed.Changed != 2 || !reflect.DeepEqual(removed.ChangedPerEntry, []int{0, 2}) ||
		removed.MemPlan.Entries[1].After[0].Place != nil || removed.MemPlan.Entries[1].After[1].Place != nil {
		t.Fatalf("remove and row deletion plan: %+v", removed)
	}
	if !reflect.DeepEqual(stateWitnessRows(removedState, "0", "work"), map[string]Decimal{"r0": "0", "r2": "2"}) {
		t.Fatalf("unexpected retained rowset: %v", stateWitnessRows(removedState, "0", "work"))
	}

	advanceOp, advanceIntent := "plan-state-advance", "plan-state-advance-v1"
	advance := Step{Epoch: "0", Space: space, Op: &advanceOp, Intent: &advanceIntent, Entries: []Entry{
		{Kind: "rowset", Table: "work", Rows: []RowRank{{Row: "r0", Rank: "0"}, {Row: "r2", Rank: "2"}}},
		{Kind: "advance", AdvanceFrom: "0"},
		{Kind: "rows", Table: "work", Add: []string{"r1"}},
		{Kind: "create", Table: "work", To: "r1:c", IDs: []string{"d"}, Scores: []string{"4"},
			Set: map[string]string{"status": "fresh"}, BeforeFields: []string{"missing"}},
	}}
	_, advancedState, advanced := stateWitnessStep(t, m, advance, [][]string{nil, nil, nil, {"d"}},
		map[string][]string{"d": {"status", "missing"}},
		map[int]rows{2: {added: []RowRank{{Row: "r1", Rank: "0"}}}})
	if advanced.EpochAfter != "1" || advancedState.ActiveEpoch != "1" ||
		!reflect.DeepEqual(advanced.ChangedPerEntry, []int{0, 0, 0, 1}) {
		t.Fatalf("advance/new epoch plan: %+v", advanced)
	}
}
