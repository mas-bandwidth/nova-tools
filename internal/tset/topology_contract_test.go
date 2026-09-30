package tset

import (
	"context"
	"reflect"
	"testing"
)

func TestS7TopologyContract(t *testing.T) {
	t.Parallel()

	t.Run("rows_union_deduplicates_and_assigns_first_occurrence_ranks", func(t *testing.T) {
		m := s7NewMem(t)
		reply := s7Step(t, m, Step{Epoch: "0", Space: s7Namespace, Entries: []Entry{
			{Kind: "rows", Table: "work", Add: []string{"one", "two"}},
			{Kind: "rows", Table: "work", Add: []string{"two", "three"}},
		}})
		if reply.Changed != 0 || !reflect.DeepEqual(reply.ChangedPerEntry, []int{0, 0}) {
			t.Fatalf("topology reply changed=%d per_entry=%v, want 0 [0 0]", reply.Changed, reply.ChangedPerEntry)
		}
		if got := s7Table(t, m).Rows; !reflect.DeepEqual(got, map[string]Decimal{"one": "0", "two": "1", "three": "2"}) {
			t.Fatalf("rows = %#v, want first-occurrence ranks", got)
		}
	})

	t.Run("row_add_and_create_share_the_prospective_topology", func(t *testing.T) {
		m := s7NewMem(t)
		s7Step(t, m, Step{Epoch: "0", Space: s7Namespace, Entries: []Entry{
			{Kind: "rows", Table: "work", Add: []string{"new"}},
			{Kind: "create", Table: "work", To: "new:c", IDs: []string{"p"}, Scores: []string{"7"}},
		}})
		table := s7Table(t, m)
		if table.Rows["new"] != "0" {
			t.Fatalf("new row rank=%q, want 0", table.Rows["new"])
		}
		s7WantRecord(t, table.Records["p"], MemRecord{Epoch: "0", Revision: "1", Row: "new", Column: "c", Score: "7", Fields: map[string]string{}})
	})

	t.Run("delete_accepts_a_row_left_empty_by_remove_in_the_same_step", func(t *testing.T) {
		m := s7NewMem(t)
		s7SeedRow(t, m, "gone", "0")
		if err := m.SeedMember(s7Namespace, "work", "0", "p", MemRecord{Epoch: "0", Revision: "1", Row: "gone", Column: "c", Score: "1", Fields: map[string]string{"keep": "yes"}}); err != nil {
			t.Fatalf("SeedMember: %v", err)
		}
		s7Step(t, m, Step{Epoch: "0", Space: s7Namespace, Entries: []Entry{
			{Kind: "rows", Table: "work", Del: []string{"gone"}},
			{Kind: "remove", Table: "work", From: "gone:c", IDs: []string{"p"}, Revs: []Decimal{"1"}},
		}})
		table := s7Table(t, m)
		if _, exists := table.Rows["gone"]; exists {
			t.Fatalf("deleted row remains: %#v", table.Rows)
		}
		s7WantRecord(t, table.Records["p"], MemRecord{Epoch: "0", Revision: "2", Fields: map[string]string{"keep": "yes"}})
	})

	t.Run("delete_with_incoming_member_refuses_without_mutation", func(t *testing.T) {
		m := s7NewMem(t)
		s7SeedRow(t, m, "old", "0")
		before, err := m.Snapshot(s7Namespace)
		if err != nil {
			t.Fatalf("Snapshot before refusal: %v", err)
		}
		_, err = m.Step(context.Background(), Step{Epoch: "0", Space: s7Namespace, Entries: []Entry{
			{Kind: "rows", Table: "work", Del: []string{"old"}},
			{Kind: "create", Table: "work", To: "old:c", IDs: []string{"incoming"}, Scores: []string{"1"}},
		}})
		s7WantRefusal(t, err, "ROWCONFLICT")
		after, err := m.Snapshot(s7Namespace)
		if err != nil {
			t.Fatalf("Snapshot after refusal: %v", err)
		}
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("delete/incoming refusal changed state\nbefore=%#v\nafter=%#v", before, after)
		}
	})
}
