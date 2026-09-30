package tset

import (
	"context"
	"encoding/json"
	"testing"
)

func TestMemAbsentRecordSelectedFieldsCountAsObservations(t *testing.T) {
	const space = "absent-fields:"
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
	check := func(step Step, wantChanged int, wantBeforePresent bool) {
		t.Helper()
		reply, err := m.Step(context.Background(), step)
		if err != nil || reply.Status != "ok" || reply.Changed != wantChanged || reply.MemPlan == nil {
			t.Fatalf("step reply=%+v err=%v", reply, err)
		}
		var counters struct {
			FieldObservations int `json:"field_observations"`
		}
		if err := json.Unmarshal(reply.Counters, &counters); err != nil {
			t.Fatal(err)
		}
		if counters.FieldObservations != 2 {
			t.Fatalf("field observations=%d, want 2 for x and y", counters.FieldObservations)
		}
		before := reply.MemPlan.Before["work"]["a"]
		if before.Exists != wantBeforePresent || len(before.Fields) != 2 ||
			before.Fields["x"].Present != wantBeforePresent || before.Fields["y"].Present {
			t.Fatalf("selected field presence: %+v", before)
		}
	}
	check(Step{Epoch: "0", Space: space, Entries: []Entry{{Kind: "create", Table: "work",
		To: "r:c", IDs: []string{"a"}, Scores: []string{"1"},
		Set: map[string]string{"x": "v"}, BeforeFields: []string{"y"}}}}, 1, false)
	// The same projection on an existing record must be counted once per field,
	// even though the requested assignment is a no-op.
	check(Step{Epoch: "0", Space: space, Entries: []Entry{{Kind: "move", Table: "work",
		From: "r:c", IDs: []string{"a"}, Set: map[string]string{"x": "v"},
		BeforeFields: []string{"y"}}}}, 0, true)
}
