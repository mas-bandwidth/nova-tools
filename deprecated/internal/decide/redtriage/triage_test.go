package redtriage

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
)

// TestMixedReportKeepsFlakyBesideReal pins the row contract: each failing
// test is classified against the flake table on its own. A report with one
// known flaky test beside one real red must give one `flaky` row and one
// `real` row, not two `real` rows because the pair was not every-flaking.
func TestMixedReportKeepsFlakyBesideReal(t *testing.T) {
	t.Parallel()

	r := ci.FailedReport{Failures: []ci.TestFailure{
		{Package: "internal/a", Test: "TestKnownFlake", At: "a_test.go:10"},
		{Package: "internal/b", Test: "TestRealBreak", At: "b_test.go:20"},
	}}
	roster := Roster{Members: []MemberPR{
		{Author: "rowan", Number: 11, Repo: "mas-bandwidth/nova-tools", Touched: []string{"a_test.go"}},
		{Author: "rowan", Number: 12, Repo: "mas-bandwidth/nova-tools", Touched: []string{"b_test.go"}},
	}}
	opt := TriageOptions{
		Flakes: []ci.FlakeRow{{Test: "TestKnownFlake", Issue: "#1", Expiry: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)}},
		Now:    "2026-09-23",
	}

	res := Triage(r, roster, opt, nil)
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(res.Rows))
	}
	got := map[string]string{}
	for _, row := range res.Rows {
		got[row.Test] = row.Class
	}
	if got["TestKnownFlake"] != ClassFlaky {
		t.Errorf("TestKnownFlake class = %q, want %q", got["TestKnownFlake"], ClassFlaky)
	}
	if got["TestRealBreak"] != ClassRed {
		t.Errorf("TestRealBreak class = %q, want %q", got["TestRealBreak"], ClassRed)
	}
	if res.Flaky != 1 || res.Real != 1 {
		t.Errorf("totals flaky=%d real=%d, want 1 and 1", res.Flaky, res.Real)
	}
}
