package fleet_test

// A bench joins the fill pool by its registry row: the `bench` role says work MAY be placed
// there, and `certified=<YYYY-MM-DD>` in the notes says it has been proven to carry a card
// end to end. Both, or it is not in the pool (#1476). The registry here is a fixture in
// t.TempDir(); no test reads the fleet's own file.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

func certifiedFixture(t *testing.T, rows ...string) *fleet.Registry {
	t.Helper()
	path := filepath.Join(t.TempDir(), "machines.tsv")
	if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := fleet.ReadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func row(name, roles, notes string) string {
	return strings.Join([]string{name, name, "linux/x64", roles, "-", "8", notes}, "\t")
}

// TestCertifiedBenchNamesIsThePool: bench role and a dated certification, in file order.
func TestCertifiedBenchNamesIsThePool(t *testing.T) {
	reg := certifiedFixture(t,
		row("bench-old", "bench", "certified=2026-09-01 the first one"),
		row("bench-new", "bench", "certified=2026-09-18 (reports/fleet/certify-bench-new.md)"),
		row("bench-raw", "bench", "provisioned, no certification run yet"),
		row("bench-undated", "bench", "certified=yes trust me"),
		row("ci-host", "runner", "certified=2026-09-18 certified, but it serves the shards"),
	)
	got := strings.Join(reg.CertifiedBenchNames(), ",")
	if want := "bench-old,bench-new"; got != want {
		t.Fatalf("the pool is %q, want %q", got, want)
	}
}

// TestCertifiedReadsTheDate: the date is the field, whatever follows it.
func TestCertifiedReadsTheDate(t *testing.T) {
	reg := certifiedFixture(t, row("bench-new", "bench", "certified=2026-09-18 (a report)"))
	m, ok := reg.Lookup("bench-new")
	if !ok {
		t.Fatal("the fixture's bench did not read")
	}
	date, certified := m.Certified()
	if !certified || date != "2026-09-18" {
		t.Fatalf("certified = (%q, %v), want (2026-09-18, true)", date, certified)
	}
}
