package tset

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// These are single-fault model fixtures. Each refused step is checked against
// the full model state: definitions, all epochs' rows/cells/records, receipts,
// arbitrary sorted sets and the active epoch. The Redis implementation needs separate
// whole-store evidence; this suite does not stand in for it.
func TestMemRefusalContract(t *testing.T) {
	t.Parallel()

	const space = "refusal:"
	const table = "work"
	base := func() Step { return Step{Epoch: "0", Space: space, Entries: []Entry{}} }
	create := func(id, to string) Entry {
		return Entry{Kind: "create", Table: table, To: to,
			IDs: []string{id}, Scores: []string{"1"}, About: []string{"primary"}}
	}
	move := func(id, from string) Entry {
		return Entry{Kind: "move", Table: table, From: from, IDs: []string{id}, About: []string{"primary"}}
	}
	two := uint64(2)
	makeCase := func(entries ...Entry) Step {
		step := base()
		step.Entries = entries
		return step
	}

	cases := []struct {
		name     string
		code     string
		step     Step
		arrange  func(*testing.T, *Mem)
		id       string
		cell     string
		row      string
		active   Decimal
		revision Decimal
	}{
		{name: "NOTABLE", code: "NOTABLE", step: makeCase(Entry{Kind: "create", Table: "absent", To: "r:c", IDs: []string{"new"}, Scores: []string{"1"}, About: []string{"primary"}})},
		{name: "STALE", code: "STALE", step: base(), active: "1",
			arrange: func(t *testing.T, m *Mem) { requireFixture(t, m.SetActiveEpoch(space, "1")) }},
		{name: "EPOCHAHEAD", code: "EPOCHAHEAD", step: func() Step {
			s := base()
			s.Epoch = "1"
			return s
		}(), active: "0"},
		{name: "NOROW", code: "NOROW", step: makeCase(create("new", "missing:c")), row: "missing"},
		{name: "NOCOL", code: "NOCOL", step: makeCase(create("new", "r:unknown")), cell: "r:unknown"},
		{name: "TWICE", code: "TWICE", step: makeCase(Entry{Kind: "create", Table: table,
			To: "r:c", IDs: []string{"new", "new"}, Scores: []string{"1", "2"}, About: []string{"primary", "primary"}}), id: "new"},
		{name: "EXISTS", code: "EXISTS", step: makeCase(create("existing", "r:c")), id: "existing"},
		{name: "MISSING", code: "MISSING", step: makeCase(move("ghost", "r:c")), id: "ghost"},
		{name: "MEMBEREPOCH", code: "MEMBEREPOCH", step: func() Step {
			s := makeCase(move("existing", "r:c"))
			s.Epoch = "1"
			return s
		}(), id: "existing", arrange: func(t *testing.T, m *Mem) {
			requireFixture(t, m.SetActiveEpoch(space, "1"))
			requireFixture(t, m.SeedRow(space, table, "1", "r", "0"))
		}},
		{name: "PLACE", code: "PLACE", step: makeCase(move("existing", "r:d")), id: "existing"},
		{name: "REVISION", code: "REVISION", step: makeCase(Entry{Kind: "move", Table: table,
			From: "r:c", IDs: []string{"existing"}, Revs: []Decimal{"2"}, About: []string{"primary"}}), id: "existing"},
		{name: "DRIFT", code: "DRIFT", step: makeCase(move("existing", "r:c")), id: "existing",
			arrange: func(t *testing.T, m *Mem) {
				requireFixture(t, m.CorruptCell(space, table, "0", "r", "c", "existing", "2"))
			}},
		{name: "CELLFULL", code: "CELLFULL", step: makeCase(Entry{Kind: "count", Table: table,
			Cells: []string{"r:c"}, CountMax: []uint64{0}}), cell: "r:c"},
		{name: "RANGECOUNT", code: "RANGECOUNT", step: makeCase(Entry{Kind: "rcount", Table: table,
			Cells: []string{"r:c"}, ScoreMin: "0", ScoreMax: "2", AtLeast: &two}), cell: "r:c"},
		{name: "PROPGUARD", code: "PROPGUARD", step: makeCase(Entry{Kind: "propguard", Table: table,
			Name: "deal_index", Value: propValue("3")})},
		{name: "OVERFLOW", code: "OVERFLOW", step: makeCase(Entry{Kind: "move", Table: table,
			From: "r:c", IDs: []string{"existing"}, Scores: []string{"2"}, About: []string{"primary"}}), id: "existing",
			revision: "18446744073709551615"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newRefusalFixture(t, space, table, tc.revision)
			if tc.arrange != nil {
				tc.arrange(t, m)
			}
			before := refusalSnapshot(t, m, space)
			_, err := m.Step(context.Background(), tc.step)
			refusal := requireRefusal(t, err, tc.code)
			if tc.id != "" && !reflect.DeepEqual(refusal.Detail.IDs, []string{tc.id}) {
				t.Errorf("IDs = %v, want [%s]", refusal.Detail.IDs, tc.id)
			}
			if tc.cell != "" && !reflect.DeepEqual(refusal.Detail.Cells, []string{tc.cell}) {
				t.Errorf("cells = %v, want [%s]", refusal.Detail.Cells, tc.cell)
			}
			if tc.row != "" && !reflect.DeepEqual(refusal.Detail.Rows, []string{tc.row}) {
				t.Errorf("rows = %v, want [%s]", refusal.Detail.Rows, tc.row)
			}
			if tc.active != "" && refusal.Detail.ActiveEpoch != tc.active {
				t.Errorf("active_epoch = %q, want %q", refusal.Detail.ActiveEpoch, tc.active)
			}
			after := refusalSnapshot(t, m, space)
			if !reflect.DeepEqual(before, after) {
				t.Errorf("refused step changed model state: before=%#v after=%#v", before, after)
			}
		})
	}

	t.Run("CONFIG definition collision", func(t *testing.T) {
		t.Parallel()
		m := newRefusalFixture(t, space, table, "")
		before := refusalSnapshot(t, m, space)
		bad := TableDefinition{Columns: []string{"c"}, MemberPrefix: space + "sprint:bad:",
			EpochKey: space + "epoch", EpochField: "current"}
		refusal := requireRefusal(t, m.DefineTable(space, "other", bad), "CONFIG")
		if refusal.Detail.Table != "other" {
			t.Errorf("table = %q, want other", refusal.Detail.Table)
		}
		if after := refusalSnapshot(t, m, space); !reflect.DeepEqual(before, after) {
			t.Errorf("refused definition changed model state: before=%#v after=%#v", before, after)
		}
		// A valid retry of the same name also proves the rejected definition did
		// not occupy its slot.
		good := TableDefinition{Columns: []string{"c"}, MemberPrefix: space + "other:",
			EpochKey: space + "epoch", EpochField: "current"}
		requireFixture(t, m.DefineTable(space, "other", good))
	})

	t.Run("CONFIG uninitialized space", func(t *testing.T) {
		t.Parallel()
		m := newRefusalFixture(t, space, table, "")
		before := refusalSnapshot(t, m, space)
		_, err := m.Step(context.Background(), Step{Epoch: "0", Space: "uninitialized:", Entries: []Entry{}})
		requireRefusal(t, err, "CONFIG")
		if after := refusalSnapshot(t, m, space); !reflect.DeepEqual(before, after) {
			t.Errorf("refused uninitialized step changed existing space: before=%#v after=%#v", before, after)
		}
		if _, err := m.Snapshot("uninitialized:"); err == nil {
			t.Error("refused step created the uninitialized space")
		} else {
			requireRefusal(t, err, "CONFIG")
		}
	})
}

func newRefusalFixture(t *testing.T, space, table string, revision Decimal) *Mem {
	t.Helper()
	if revision == "" {
		revision = "3"
	}
	m := NewMem()
	requireFixture(t, m.DefineTable(space, table, TableDefinition{
		Columns: []string{"c", "d"}, MemberPrefix: space + "member:",
		EpochKey: space + "epoch", EpochField: "current",
	}))
	requireFixture(t, m.SeedRow(space, table, "0", "r", "0"))
	requireFixture(t, m.SeedMember(space, table, "0", "existing", MemRecord{
		Epoch: "0", Revision: revision, Row: "r", Column: "c", Score: "1",
		Fields: map[string]string{"state": "live"},
	}))
	return m
}

func refusalSnapshot(t *testing.T, m *Mem, space string) MemSnapshot {
	t.Helper()
	snapshot, err := m.Snapshot(space)
	requireFixture(t, err)
	return snapshot
}

func requireFixture(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("fixture setup: %v", err)
	}
}

func requireRefusal(t *testing.T, err error, code string) *Refusal {
	t.Helper()
	var refusal *Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("error = %v, want %s refusal", err, code)
	}
	if refusal.Status != "refused" || refusal.Code != code ||
		!strings.HasSuffix(refusal.Message, "; nothing was changed") {
		t.Fatalf("refusal = %#v, want %s with no-change message", refusal, code)
	}
	return refusal
}
