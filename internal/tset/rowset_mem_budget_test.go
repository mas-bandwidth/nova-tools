package tset

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
)

func TestMemRowsetReadWorkCarriesIntoPlan(t *testing.T) {
	const space = "rowset-budget:"
	m := NewMem()
	if err := m.DefineTable(space, "work", TableDefinition{
		Columns: []string{"c"}, MemberPrefix: space + "member:work:",
		EpochKey: space + "sprint:epoch", EpochField: "n",
	}); err != nil {
		t.Fatal(err)
	}
	rows := make([]RowRank, 1001)
	wantFetched := len("1001") // ZCARD's decimal reply.
	for i := range rows {
		row, rank := fmt.Sprintf("r%04d", i), strconv.Itoa(i)
		rows[i] = RowRank{Row: row, Rank: Decimal(rank)}
		wantFetched += len(rank) // Two ZMSCORE reply pieces, 1000 + 1.
		if err := m.SeedRow(space, "work", "0", row, Decimal(rank)); err != nil {
			t.Fatal(err)
		}
	}
	reply, err := m.Step(context.Background(), Step{Epoch: "0", Space: space, Entries: []Entry{
		{Kind: "rowset", Table: "work", Rows: rows},
		{Kind: "advance", AdvanceFrom: "0"},
		{Kind: "rows", Table: "work", Add: []string{"fresh"}},
		{Kind: "create", Table: "work", To: "fresh:c", IDs: []string{"new"}, Scores: []string{"1"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var work struct {
		CellProbes   int `json:"cell_probes"`
		FetchedBytes int `json:"raw_fetched_bytes"`
	}
	if err := json.Unmarshal(reply.Counters, &work); err != nil {
		t.Fatal(err)
	}
	// ZCARD + ceil(1001/1000) ZMSCORE + the ordinary create probe.
	if work.CellProbes != 4 || work.FetchedBytes != wantFetched {
		t.Fatalf("rowset work not carried into plan: %+v, want 4 probes and %d fetched bytes", work, wantFetched)
	}
	if reply.Guarded != 0 || reply.Changed != 1 || len(reply.ChangedPerEntry) != 4 ||
		reply.ChangedPerEntry[0] != 0 || reply.ChangedPerEntry[1] != 0 || reply.ChangedPerEntry[2] != 0 || reply.ChangedPerEntry[3] != 1 {
		t.Fatalf("rowset changed member accounting: %+v", reply)
	}
}
