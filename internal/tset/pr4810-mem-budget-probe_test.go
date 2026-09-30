// External draft only. To run later, copy into internal/tset/ after repair ownership.
package tset

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func newPropertyBudgetMem(t *testing.T) (*Mem, string) {
	t.Helper()
	const space = "prop-budget:"
	m := NewMem()
	if err := m.DefineTable(space, "fleet", TableDefinition{
		Columns: []string{"c"}, MemberPrefix: space + "member:",
		EpochKey: space + "epoch", EpochField: "n",
	}); err != nil {
		t.Fatal(err)
	}
	return m, space
}

func propertyBudgetCounters(t *testing.T, reply Reply) (cellProbes, fetchedBytes int) {
	t.Helper()
	var counts struct {
		CellProbes   int `json:"cell_probes"`
		FetchedBytes int `json:"raw_fetched_bytes"`
	}
	if err := json.Unmarshal(reply.Counters, &counts); err != nil {
		t.Fatal(err)
	}
	return counts.CellProbes, counts.FetchedBytes
}

func TestMemPropertyHLENMustEnforceCellCap(t *testing.T) {
	t.Parallel()
	m, space := newPropertyBudgetMem(t)
	cells := make([]string, 20000)
	maxima := make([]uint64, len(cells)) // all cells empty, so max=0 passes
	for i := range cells {
		row := fmt.Sprintf("r%05d", i)
		if err := m.SeedRow(space, "fleet", "0", row, Decimal(fmt.Sprint(i))); err != nil {
			t.Fatal(err)
		}
		cells[i] = row + ":c"
	}
	count := Entry{Kind: "count", Table: "fleet", Cells: cells, CountMax: maxima}
	plain := Step{Epoch: "0", Space: space, Entries: []Entry{count}}
	reply, err := m.Step(context.Background(), plain)
	if err != nil {
		t.Fatalf("legal count-only control: %v", err)
	}
	if probes, _ := propertyBudgetCounters(t, reply); probes != 20000 {
		t.Fatalf("count-only cell probes = %d, want 20000", probes)
	}
	before, err := m.Snapshot(space)
	if err != nil {
		t.Fatal(err)
	}
	value := "v"
	mixed := Step{Epoch: "0", Space: space, Entries: []Entry{
		count, {Kind: "prop", Table: "fleet", Name: "p", Value: &value},
	}}
	mixedReply, err := m.Step(context.Background(), mixed)
	after, snapshotErr := m.Snapshot(space)
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}
	unchanged := reflect.DeepEqual(before, after)
	var mixedProbes, mixedFetched int
	if err == nil {
		mixedProbes, mixedFetched = propertyBudgetCounters(t, mixedReply)
	}
	refusal, ok := err.(*Refusal)
	if !ok || refusal.Code != "LIMIT" || refusal.Detail.Budget != "cell_probes" {
		t.Fatalf("20,001st probe must refuse LIMIT/cell_probes: err=%v mixed_reply=%+v cell_probes=%d raw_fetched_bytes=%d unchanged=%t property=%q",
			err, mixedReply, mixedProbes, mixedFetched, unchanged, after.Epochs["0"].Tables["fleet"].Props["p"])
	}
	if !unchanged {
		t.Fatal("cell-cap refusal published property or other state")
	}
}

func TestMemPropertyHLENAccountsNumericFetchedByte(t *testing.T) {
	t.Parallel()
	m, space := newPropertyBudgetMem(t)
	value := "v"
	reply, err := m.Step(context.Background(), Step{Epoch: "0", Space: space,
		Entries: []Entry{{Kind: "prop", Table: "fleet", Name: "p", Value: &value}}})
	if err != nil {
		t.Fatal(err)
	}
	_, fetched := propertyBudgetCounters(t, reply)
	// The absent HGET returns no bytes. HLEN on the empty property hash
	// returns integer 0, whose Lua payload is tostring(0), one byte.
	if fetched < 1 {
		t.Fatalf("raw_fetched_bytes = %d, omits HLEN reply byte", fetched)
	}
}
