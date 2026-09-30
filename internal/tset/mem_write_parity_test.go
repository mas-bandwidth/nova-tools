package tset

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func writeParityMem(t *testing.T) *Mem {
	t.Helper()
	const space = "write-parity:"
	m := NewMem()
	if err := m.DefineTable(space, "work", TableDefinition{Columns: []string{"c", "d"}, MemberPrefix: space + "member:work:", EpochKey: space + "sprint:epoch", EpochField: "n"}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ name, rank string }{{"r", "0"}, {"s", "1"}} {
		if err := m.SeedRow(space, "work", "0", row.name, Decimal(row.rank)); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.SeedMember(space, "work", "0", "p", MemRecord{Epoch: "0", Revision: "1", Row: "r", Column: "c", Score: "1", Fields: map[string]string{"state": "live"}}); err != nil {
		t.Fatal(err)
	}
	return m
}

func assertMemWriteRefusal(t *testing.T, m *Mem, step Step, code string, entry int, rows, ids []string) {
	t.Helper()
	before, err := m.Snapshot(step.Space)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := m.Step(context.Background(), step)
	got := requireRefusal(t, err, code)
	if reply.MemPlan != nil || got.Detail.EntryIndex == nil || *got.Detail.EntryIndex != entry || got.Detail.Table != "work" ||
		!reflect.DeepEqual(got.Detail.Rows, rows) || !reflect.DeepEqual(got.Detail.IDs, ids) || len(got.Detail.Cells) != 0 {
		t.Fatalf("%s reply/detail = %+v/%+v", code, reply, got.Detail)
	}
	after, err := m.Snapshot(step.Space)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("%s mutated state: err=%v before=%+v after=%+v", code, err, before, after)
	}
}

func TestMemDeletedRowChangedAndUnchangedStays(t *testing.T) {
	t.Parallel()
	const space = "write-parity:"
	for _, tc := range []struct {
		name, to string
		scores   []string
		set      map[string]string
		code     string
		index    int
	}{
		{"changed implicit", "", []string{"2"}, nil, "ROWCONFLICT", 1},
		{"changed explicit", "r:c", nil, map[string]string{"state": "ready"}, "ROWCONFLICT", 1},
		{"unchanged implicit", "", nil, nil, "OCCUPIED", 0},
		{"unchanged explicit", "r:c", nil, nil, "OCCUPIED", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := writeParityMem(t)
			step := Step{Epoch: "0", Space: space, Entries: []Entry{
				{Kind: "rows", Table: "work", Del: []string{"r"}},
				{Kind: "move", Table: "work", From: "r:c", To: tc.to, IDs: []string{"p"}, Scores: tc.scores, Set: tc.set},
			}}
			assertMemWriteRefusal(t, m, step, tc.code, tc.index, []string{"r"}, nil)
		})
	}
}

func TestMemDestinationAlreadyContainsIDIsDrift(t *testing.T) {
	t.Parallel()
	const space = "write-parity:"
	for _, tc := range []struct{ name, kind, id string }{{"create", "create", "new"}, {"move", "move", "p"}} {
		t.Run(tc.name, func(t *testing.T) {
			m := writeParityMem(t)
			if err := m.SeedMember(space, "work", "0", "q", MemRecord{Epoch: "0", Revision: "1", Row: "s", Column: "d", Score: "3"}); err != nil {
				t.Fatal(err)
			}
			if err := m.CorruptCell(space, "work", "0", "s", "d", tc.id, "2"); err != nil {
				t.Fatal(err)
			}
			entry := Entry{Kind: tc.kind, Table: "work", To: "s:d", IDs: []string{tc.id}, Scores: []string{"2"}}
			if tc.kind == "move" {
				entry.From = "r:c"
			}
			assertMemWriteRefusal(t, m, Step{Epoch: "0", Space: space, Entries: []Entry{entry}}, "DRIFT", 0, nil, []string{tc.id})
		})
	}
}

func TestMemStepsPreserveRawRequestsAndPreflight(t *testing.T) {
	t.Parallel()
	const space = "write-parity:"
	m := writeParityMem(t)
	steps := []Step{
		{Epoch: "0", Space: space, Entries: []Entry{{Kind: "move", Table: "work", From: "r:c", IDs: []string{"p"}}}},
		{Epoch: "0", Space: space, Entries: []Entry{{Kind: "create", Table: "work", To: "missing:c", IDs: []string{"x"}, Scores: []string{"1"}}}},
	}
	want := make([][]byte, len(steps))
	for i, step := range steps {
		var err error
		want[i], err = EncodeStep(step)
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := m.Steps(context.Background(), steps)
	if err != nil || len(got) != len(steps) {
		t.Fatalf("Steps: slots=%d err=%v", len(got), err)
	}
	for i := range got {
		if !reflect.DeepEqual(got[i].RawRequest, want[i]) {
			t.Fatalf("slot %d raw=%q want=%q", i, got[i].RawRequest, want[i])
		}
	}
	if got[0].Err != nil {
		t.Fatalf("successful slot: %v", got[0].Err)
	}
	requireRefusal(t, got[1].Err, "NOROW")
	before, err := m.Snapshot(space)
	if err != nil {
		t.Fatal(err)
	}
	bad := append([]Step(nil), steps...)
	bad[1] = Step{Epoch: "0", Space: space, Entries: []Entry{{Kind: "unknown", Table: "work"}}}
	if slots, err := m.Steps(context.Background(), bad); err == nil || !strings.HasPrefix(err.Error(), "step 1:") || slots != nil {
		t.Fatalf("preflight slots=%+v err=%v", slots, err)
	}
	after, err := m.Snapshot(space)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed preflight mutated state: %v", err)
	}
}

func TestMemRecordLocationIndexTracksRetainedEpochsAndAtomicPublication(t *testing.T) {
	t.Parallel()
	const space = "write-parity:"
	m := writeParityMem(t)
	m.mu.Lock()
	owner, epoch, found := m.spaces[space].recordTable("work", "p")
	if !found || epoch != "0" || owner.records["p"] == nil {
		m.mu.Unlock()
		t.Fatalf("seed index: found=%t epoch=%s", found, epoch)
	}
	clone := cloneMemSpace(m.spaces[space])
	clone.recordEpoch["work"]["p"] = "9"
	if m.spaces[space].recordEpoch["work"]["p"] != "0" {
		m.mu.Unlock()
		t.Fatal("clone aliases index")
	}
	m.mu.Unlock()
	advanceOp, advanceIntent := "write-parity-advance", "write-parity-advance-v1"
	advance := Step{Epoch: "0", Space: space, Op: &advanceOp, Intent: &advanceIntent, Entries: []Entry{{Kind: "advance", AdvanceFrom: "0"}, {Kind: "rows", Table: "work", Add: []string{"new"}}, {Kind: "create", Table: "work", To: "new:c", IDs: []string{"fresh"}, Scores: []string{"4"}}}}
	if _, err := m.Step(context.Background(), advance); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	_, oldEpoch, oldFound := m.spaces[space].recordTable("work", "p")
	newOwner, newEpoch, newFound := m.spaces[space].recordTable("work", "fresh")
	m.mu.Unlock()
	if !oldFound || oldEpoch != "0" || !newFound || newEpoch != "1" || newOwner.records["fresh"].epoch != "1" {
		t.Fatalf("retained/create index: old=%t/%s new=%t/%s", oldFound, oldEpoch, newFound, newEpoch)
	}
	before, err := m.Snapshot(space)
	if err != nil {
		t.Fatal(err)
	}
	failed := Step{Epoch: "1", Space: space, Entries: []Entry{{Kind: "create", Table: "work", To: "new:c", IDs: []string{"rolled-back"}, Scores: []string{"1"}}, {Kind: "create", Table: "work", To: "absent:c", IDs: []string{"bad"}, Scores: []string{"1"}}}}
	_, err = m.Step(context.Background(), failed)
	requireRefusal(t, err, "NOROW")
	m.mu.Lock()
	_, _, found = m.spaces[space].recordTable("work", "rolled-back")
	m.mu.Unlock()
	after, snapshotErr := m.Snapshot(space)
	if found || snapshotErr != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("refusal published index/state: found=%t err=%v", found, snapshotErr)
	}
}
