package tset

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"testing"
)

func rowsetRanks(count int) []RowRank {
	rows := make([]RowRank, count)
	for i := range rows {
		rows[i] = RowRank{Row: fmt.Sprintf("r%04d", i), Rank: Decimal(strconv.Itoa(i))}
	}
	return rows
}

func rowsetNames(rows []RowRank) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.Row
	}
	return out
}

func rowsetAdvance(space string, expected []RowRank, restore []string) Step {
	// An allocated empty slice is the meaningful empty-set assertion.
	rows := append([]RowRank{}, expected...)
	entries := []Entry{{Kind: "rowset", Table: "work", Rows: rows},
		{Kind: "advance", AdvanceFrom: "0"}}
	if restore != nil {
		entries = append(entries, Entry{Kind: "rows", Table: "work", Add: append([]string{}, restore...)})
	}
	op, intent := fmt.Sprintf("rowset-clear-%d", len(expected)), "clear rows after checking the expected topology"
	return Step{Epoch: "0", Space: space, Op: &op, Intent: &intent, Entries: entries}
}

func newRowsetMem(t *testing.T, space string, actual []RowRank) *Mem {
	t.Helper()
	m := NewMem()
	if err := m.DefineTable(space, "work", TableDefinition{
		Columns: []string{"c", "d"}, MemberPrefix: space + "member:work:",
		EpochKey: space + "sprint:epoch", EpochField: "n",
	}); err != nil {
		t.Fatalf("define rowset table: %v", err)
	}
	for _, row := range actual {
		if err := m.SeedRow(space, "work", "0", row.Row, row.Rank); err != nil {
			t.Fatalf("seed row %q: %v", row.Row, err)
		}
	}
	return m
}

func rowsetMemSnapshot(t *testing.T, m *Mem, space string) MemSnapshot {
	t.Helper()
	snapshot, err := m.Snapshot(space)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestMemRowsetCompleteTopology(t *testing.T) {
	t.Parallel()
	base := rowsetRanks(2)
	for _, tc := range []struct {
		name     string
		actual   []RowRank
		expected []RowRank
		wantCode string
		wantRow  string
	}{
		{name: "exact ranks in different wire order", actual: base,
			expected: []RowRank{base[1], base[0]}},
		{name: "empty matches empty", actual: []RowRank{}, expected: []RowRank{}},
		{name: "extra row", actual: base, expected: base[:1], wantCode: "ROWSET"},
		{name: "missing row", actual: base[:1], expected: base, wantCode: "ROWSET"},
		{name: "equal-cardinality replacement", actual: base,
			expected: []RowRank{base[0], {Row: "replacement", Rank: "1"}},
			wantCode: "ROWSET", wantRow: "replacement"},
		{name: "rank changed", actual: base,
			expected: []RowRank{{Row: base[0].Row, Rank: "7"}, base[1]},
			wantCode: "ROWSET", wantRow: base[0].Row},
		{name: "first row races empty clear", actual: base[:1], expected: []RowRank{}, wantCode: "ROWSET"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const space = "rowset:"
			m := newRowsetMem(t, space, tc.actual)
			before := rowsetMemSnapshot(t, m, space)
			step := rowsetAdvance(space, tc.expected, nil)
			reply, err := m.Step(context.Background(), step)
			if tc.wantCode != "" {
				refusal := requireRefusal(t, err, tc.wantCode)
				if refusal.Detail.EntryIndex == nil || *refusal.Detail.EntryIndex != 0 ||
					refusal.Detail.Table != "work" || refusal.Detail.ActiveEpoch != "0" {
					t.Fatalf("ROWSET detail=%+v, want index 0, work, active epoch 0", refusal.Detail)
				}
				if tc.wantRow != "" && !reflect.DeepEqual(refusal.Detail.Rows, []string{tc.wantRow}) {
					t.Fatalf("ROWSET rows detail=%v, want [%s]", refusal.Detail.Rows, tc.wantRow)
				}
				if after := rowsetMemSnapshot(t, m, space); !reflect.DeepEqual(before, after) {
					t.Fatalf("ROWSET refusal changed full Mem state: before=%#v after=%#v", before, after)
				}
				return
			}
			if err != nil || reply.Status != "ok" || reply.EpochAfter != "1" ||
				reply.Changed != 0 || reply.Guarded != 0 ||
				!reflect.DeepEqual(reply.ChangedPerEntry, []int{0, 0}) {
				t.Fatalf("exact rowset advance reply=%+v err=%v", reply, err)
			}
			after := rowsetMemSnapshot(t, m, space)
			if after.ActiveEpoch != "1" ||
				!reflect.DeepEqual(after.Epochs["0"].Tables["work"].Rows, before.Epochs["0"].Tables["work"].Rows) ||
				len(after.Epochs["1"].Tables["work"].Rows) != 0 {
				t.Fatalf("exact guard changed old rows or copied them implicitly: %+v", after)
			}
		})
	}
}

func TestMemRowsetAdvancePairUnionBoundary(t *testing.T) {
	t.Parallel()
	for _, size := range []int{604, 1024} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			const space = "rowset:"
			rows := rowsetRanks(size)
			m := newRowsetMem(t, space, rows)
			step := rowsetAdvance(space, rows, rowsetNames(rows))
			reply, err := m.Step(context.Background(), step)
			if err != nil || reply.Status != "ok" || reply.EpochAfter != "1" ||
				reply.Changed != 0 || reply.Guarded != 0 ||
				!reflect.DeepEqual(reply.ChangedPerEntry, []int{0, 0, 0}) {
				t.Fatalf("%d guarded/restored row union reply=%+v err=%v", size, reply, err)
			}
			if got := len(rowsetMemSnapshot(t, m, space).Epochs["1"].Tables["work"].Rows); got != size {
				t.Fatalf("%d guarded/restored rows produced %d", size, got)
			}
		})
	}
	const space = "rowset:"
	rows := rowsetRanks(1024)
	m := newRowsetMem(t, space, rows)
	before := rowsetMemSnapshot(t, m, space)
	step := rowsetAdvance(space, rows, rowsetNames(rows))
	step.Entries = append(step.Entries, Entry{Kind: "rows", Table: "work", Add: []string{"extra"}})
	_, err := m.Step(context.Background(), step)
	requireRefusal(t, err, "LIMIT")
	if after := rowsetMemSnapshot(t, m, space); !reflect.DeepEqual(before, after) {
		t.Fatal("1025 distinct guarded/restored row names mutated Mem state")
	}
}

func TestMemRowsetReceiptReplayBypassesChangedExpectation(t *testing.T) {
	t.Parallel()
	const space = "rowset:"
	rows := rowsetRanks(1)
	m := newRowsetMem(t, space, rows)
	op, intent := "clear-0", "stable clear arguments"
	original := rowsetAdvance(space, rows, rowsetNames(rows))
	original.Op, original.Intent, original.Result = &op, &intent, "stored result"
	fresh, err := m.Step(context.Background(), original)
	if err != nil || fresh.Replay || fresh.EpochAfter != "1" {
		t.Fatalf("named advance: reply=%+v err=%v", fresh, err)
	}
	before := rowsetMemSnapshot(t, m, space)
	replanned := rowsetAdvance(space, []RowRank{{Row: "not-in-old-epoch", Rank: "0"}}, []string{"not-in-old-epoch"})
	replanned.Op, replanned.Intent, replanned.Result = &op, &intent, "new estimate"
	replay, err := m.Step(context.Background(), replanned)
	if err != nil || !replay.Replay || replay.Result != "stored result" ||
		replay.EpochBefore != "0" || replay.EpochAfter != "1" {
		t.Fatalf("changed valid rowset expectation failed to replay original receipt: %+v, %v", replay, err)
	}
	if after := rowsetMemSnapshot(t, m, space); !reflect.DeepEqual(before, after) {
		t.Fatal("rowset receipt replay changed full Mem state")
	}
}
