package tset

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

const s7Namespace = "s7:"

func TestS7FieldContract(t *testing.T) {
	t.Parallel()

	t.Run("shared_each_unset_effective_deltas_and_noop_revisions", func(t *testing.T) {
		m := s7NewMem(t)
		s7SeedRow(t, m, "r", "0")
		s7Step(t, m, Step{
			Epoch: "0", Space: s7Namespace,
			Entries: []Entry{{
				Kind: "create", Table: "work", To: "r:c",
				IDs: []string{"a", "b"}, Scores: []string{"1", "2"},
				Set: map[string]string{"common": "shared", "override": "shared", "empty": ""},
				Each: []map[string]string{
					{"override": "a", "only": "one"},
					{"only": "two"},
				},
			}},
		})

		got := s7Table(t, m).Records
		s7WantRecord(t, got["a"], MemRecord{
			Epoch: "0", Revision: "1", Row: "r", Column: "c", Score: "1",
			Fields: map[string]string{"common": "shared", "override": "a", "only": "one", "empty": ""},
		})
		s7WantRecord(t, got["b"], MemRecord{
			Epoch: "0", Revision: "1", Row: "r", Column: "c", Score: "2",
			Fields: map[string]string{"common": "shared", "override": "shared", "only": "two", "empty": ""},
		})

		noChange := s7Step(t, m, Step{
			Epoch: "0", Space: s7Namespace,
			Entries: []Entry{{
				Kind: "move", Table: "work", From: "r:c", To: "r:c",
				IDs: []string{"a", "b"}, Scores: []string{"1.0", "2.0"}, Revs: []Decimal{"1", "1"},
				Set:  map[string]string{"common": "shared", "override": "shared", "empty": ""},
				Each: []map[string]string{{"override": "a", "only": "one"}, {"only": "two"}},
			}},
		})
		if noChange.Changed != 0 || !reflect.DeepEqual(noChange.ChangedPerEntry, []int{0}) {
			t.Fatalf("no-op reply changed=%d per_entry=%v, want 0 [0]", noChange.Changed, noChange.ChangedPerEntry)
		}
		got = s7Table(t, m).Records
		if got["a"].Revision != "1" || got["b"].Revision != "1" {
			t.Fatalf("no-op revisions = a:%s b:%s, want both 1", got["a"].Revision, got["b"].Revision)
		}

		removedEmpty := s7Step(t, m, Step{
			Epoch: "0", Space: s7Namespace,
			Entries: []Entry{{
				Kind: "move", Table: "work", From: "r:c", To: "r:c", IDs: []string{"a", "b"}, Revs: []Decimal{"1", "1"},
				Unset: []string{"empty", "absent"}, BeforeFields: []string{"empty", "absent"},
			}},
		})
		if removedEmpty.Changed != 2 {
			t.Fatalf("unset empty/absent changed=%d, want 2 because empty is present", removedEmpty.Changed)
		}
		got = s7Table(t, m).Records
		for _, id := range []string{"a", "b"} {
			if _, present := got[id].Fields["empty"]; present {
				t.Fatalf("%s empty field remained after unset: %#v", id, got[id].Fields)
			}
			if got[id].Revision != "2" {
				t.Fatalf("%s revision=%s after removing present empty field, want 2", id, got[id].Revision)
			}
		}
	})

	t.Run("remove_keeps_unmentioned_fields_and_records_retirement", func(t *testing.T) {
		m := s7NewMem(t)
		s7SeedRow(t, m, "r", "0")
		s7Step(t, m, Step{Epoch: "0", Space: s7Namespace, Entries: []Entry{{
			Kind: "create", Table: "work", To: "r:c", IDs: []string{"p"}, Scores: []string{"3"},
			Set: map[string]string{"keep": "yes", "replace": "old"},
		}}})

		reply := s7Step(t, m, Step{Epoch: "0", Space: s7Namespace, Entries: []Entry{{
			Kind: "remove", Table: "work", From: "r:c", IDs: []string{"p"}, Revs: []Decimal{"1"},
			Set: map[string]string{"retired": "true", "retired_by": "review", "replace": "new"},
		}}})
		if reply.Changed != 1 {
			t.Fatalf("remove changed=%d, want 1", reply.Changed)
		}
		table := s7Table(t, m)
		s7WantRecord(t, table.Records["p"], MemRecord{
			Epoch: "0", Revision: "2",
			Fields: map[string]string{"keep": "yes", "replace": "new", "retired": "true", "retired_by": "review"},
		})
		if members := table.Cells["r"]["c"]; len(members) != 0 {
			t.Fatalf("removed member remained in source cell: %#v", members)
		}
	})

	t.Run("same_cell_move_stays_once_and_can_change_score", func(t *testing.T) {
		m := s7NewMem(t)
		s7SeedRow(t, m, "r", "0")
		s7SeedRow(t, m, "other", "1")
		s7Step(t, m, Step{Epoch: "0", Space: s7Namespace, Entries: []Entry{{
			Kind: "create", Table: "work", To: "r:c", IDs: []string{"p"}, Scores: []string{"1"},
		}}})

		s7Step(t, m, Step{Epoch: "0", Space: s7Namespace, Entries: []Entry{{
			Kind: "move", Table: "work", From: "r:c", To: "r:c", IDs: []string{"p"}, Scores: []string{"2"}, Revs: []Decimal{"1"},
		}}})
		table := s7Table(t, m)
		s7WantRecord(t, table.Records["p"], MemRecord{Epoch: "0", Revision: "2", Row: "r", Column: "c", Score: "2", Fields: map[string]string{}})
		if got := table.Cells["r"]["c"]; !reflect.DeepEqual(got, map[string]string{"p": "2"}) {
			t.Fatalf("same-cell membership = %#v, want only p at score 2", got)
		}
		if members := table.Cells["other"]["c"]; len(members) != 0 {
			t.Fatalf("same-cell stay wrote another cell: %#v", members)
		}
	})
}

func s7NewMem(t *testing.T) *Mem {
	t.Helper()
	m := NewMem()
	if err := m.DefineTable(s7Namespace, "work", TableDefinition{
		Columns: []string{"c", "d"}, MemberPrefix: s7Namespace + "member:", EpochKey: s7Namespace + "epoch", EpochField: "current",
	}); err != nil {
		t.Fatalf("DefineTable: %v", err)
	}
	return m
}

func s7SeedRow(t *testing.T, m *Mem, row string, rank Decimal) {
	t.Helper()
	if err := m.SeedRow(s7Namespace, "work", "0", row, rank); err != nil {
		t.Fatalf("SeedRow %q: %v", row, err)
	}
}

func s7Step(t *testing.T, m *Mem, step Step) Reply {
	t.Helper()
	reply, err := m.Step(context.Background(), step)
	if err != nil {
		t.Fatalf("Step(%#v): %v", step, err)
	}
	return reply
}

func s7Table(t *testing.T, m *Mem) MemTableSnapshot {
	t.Helper()
	snapshot, err := m.Snapshot(s7Namespace)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return snapshot.Epochs["0"].Tables["work"]
}

func s7WantRecord(t *testing.T, got, want MemRecord) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("record = %#v, want %#v", got, want)
	}
}

func s7WantRefusal(t *testing.T, err error, code string) {
	t.Helper()
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != code {
		t.Fatalf("refusal = %v, want %s", err, code)
	}
}
