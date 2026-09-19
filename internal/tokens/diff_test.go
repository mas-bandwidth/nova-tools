package tokens

import (
	"strings"
	"testing"
)

// TestTokensDiffLargestMovement covers the diff comparator: rows are compared by key,
// sorted by absolute movement descending, a side missing an entry is a dash, and the
// delta is the signed new-minus-old when both sides are present.
func TestTokensDiffLargestMovement(t *testing.T) {
	oldEntries := map[string]int64{
		"schema": 100,
		"cli":    50,
		"gone":   30,
	}
	newEntries := map[string]int64{
		"schema":   60,
		"cli":      55,
		"newcomer": 200,
	}

	rows := ComputeTokensDiff(oldEntries, newEntries)

	// The largest movement is first: newcomer +200, then schema -40, then gone -30,
	// then cli +5.
	wantOrder := []string{"newcomer", "schema", "gone", "cli"}
	if len(rows) != len(wantOrder) {
		t.Fatalf("%d rows, want %d: %v", len(rows), len(wantOrder), rows)
	}
	for i, key := range wantOrder {
		if rows[i].Key != key {
			t.Errorf("row %d is %q, want %q (largest movement first)", i, rows[i].Key, key)
		}
	}

	// A key absent on the old side shows a dash for old tokens and a positive delta.
	if rows[0].Old != Dash || rows[0].New != "200" || rows[0].Delta != 200 {
		t.Errorf("newcomer row is %+v, want old=- new=200 delta=+200", rows[0])
	}

	// A key absent on the new side shows a dash for new tokens and a negative delta.
	if rows[2].Key != "gone" || rows[2].Old != "30" || rows[2].New != Dash || rows[2].Delta != -30 {
		t.Errorf("gone row is %+v, want old=30 new=- delta=-30", rows[2])
	}

	// Both sides present: a correct signed delta, new minus old.
	if rows[1].Old != "100" || rows[1].New != "60" || rows[1].Delta != -40 {
		t.Errorf("schema row is %+v, want old=100 new=60 delta=-40", rows[1])
	}
	if rows[3].Old != "50" || rows[3].New != "55" || rows[3].Delta != 5 {
		t.Errorf("cli row is %+v, want old=50 new=55 delta=+5", rows[3])
	}

	// The rendered form keeps the same order and shows a dash on each missing side.
	out := FormatDiff(rows)
	lines := strings.Split(out, "\n")
	if len(lines) != 4 {
		t.Fatalf("Formatted %d lines, want 4:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], "newcomer\t-\t200\t+200") {
		t.Errorf("first line is %q, want newcomer - 200 +200", lines[0])
	}
	if !strings.Contains(lines[2], "gone\t30\t-\t-30") {
		t.Errorf("third line is %q, want gone 30 - -30", lines[2])
	}
}
