package pulse

// Card 8361: nova-pulse run folds every root's pool usage into the monthly ledger once per
// tick, with nova-tokens fold-pool semantics (internal/tokens, never a shell-out). The
// fold is idempotent by construction -- fold-pool upserts -- so a tick that changes nothing
// prints nothing, and a quiet pulse stays quiet.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// usageHeader is the sixteen SPEC-SWARM rule 12 columns, in order.
const runUsageHeader = "job\tattempt\tfrom\tstarted\tended\tend\trc\tprovider\tmodel\trepo\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd"

// writeRunUsage lays one usage file under <root>/pool/usage/, the layout the swarm leaves.
func writeRunUsage(t *testing.T, path, job, model, in, out, usd string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	row := strings.Join([]string{
		job, "1", "swarm", "2026-09-16T10:00:00Z", "2026-09-16T10:01:00Z", "done", "0",
		"deepseek", model, "r1", in, out, "10", "20", "5", usd,
	}, "\t")
	if err := os.WriteFile(path, []byte(runUsageHeader+"\n"+row+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRunFoldsPoolUsageIntoMonthlyLedger: after harvest, a tick folds each root's
// pool/usage into <queue>/ledger-<YYYY-MM>.tsv and prints one FOLD line only when the
// ledger's rows changed. The second tick folds the same usage, changes nothing, and stays
// silent with the ledger byte-identical.
func TestRunFoldsPoolUsageIntoMonthlyLedger(t *testing.T) {
	queue := t.TempDir()
	root := t.TempDir()
	writeRunUsage(t, filepath.Join(root, "pool", "usage", "job-a.tsv"), "job-a", "m1", "100", "50", "0.010000")
	writeRunUsage(t, filepath.Join(root, "pool", "usage", "job-b.tsv"), "job-b", "m2", "200", "75", "0.020000")

	now := time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC)
	run := func() string {
		var out, errs bytes.Buffer
		if exit := Run(RunInput{
			Queue: queue, Roots: root, Once: true, GateEvery: 99,
			Stdout: &out, Stderr: &errs,
			Now: func() time.Time { return now },
		}); exit != 0 {
			t.Fatalf("exit %d, want 0: %s%s", exit, out.String(), errs.String())
		}
		return out.String()
	}

	first := run()
	if !strings.Contains(first, "FOLD rows=2 tasks=2 ledger=") || !strings.Contains(first, "ledger-2026-09.tsv") {
		t.Fatalf("the first tick must fold the pool usage into the monthly ledger and print FOLD:\n%s", first)
	}
	ledger := filepath.Join(queue, "ledger-2026-09.tsv")
	before, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("the first tick did not write the ledger: %v", err)
	}

	second := run()
	if strings.Contains(second, "FOLD ") {
		t.Errorf("the second tick must be silent; the ledger did not change:\n%s", second)
	}
	after, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("the second tick removed the ledger: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("the second tick changed the ledger:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
