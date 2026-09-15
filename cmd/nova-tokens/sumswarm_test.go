package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// cardUsageHeader is the thirteen columns of one card's usage.tsv, transcribed from the
// contract named in internal/swarm/usagecard.go (job, attempt, started, ended, rc, provider,
// model, and the five token types plus usd) and written here from that text, never from a
// constant, so the fixture can disagree with the reader it is meant to check.
const cardUsageHeader = "job\tattempt\tstarted\tended\trc\tprovider\tmodel\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd"

// cardUsageFile writes one card's usage.tsv: the header and one row.
func cardUsageFile(t *testing.T, path, provider, model, started, in, out, usd string) string {
	t.Helper()
	row := strings.Join([]string{"c", "1", started, started, "0", provider, model, in, out, "-", "-", "-", usd}, "\t")
	return write(t, path, cardUsageHeader+"\n"+row+"\n")
}

func TestSumSwarmRootIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	root := mkdir(t, filepath.Join(dir, "root"))
	ledger := filepath.Join(dir, "ledger.tsv")

	// Two cards for the model deepseek-v4 on the day, and one card on a different day.
	cardUsageFile(t, filepath.Join(root, "batch-a", "jobs", "j1", "usage.tsv"),
		"deepseek", "deepseek-v4", "2026-09-11T10:00:00Z", "1000", "200", "0.0100")
	cardUsageFile(t, filepath.Join(root, "batch-b", "jobs", "j2", "usage.tsv"),
		"deepseek", "deepseek-v4", "2026-09-11T11:00:00Z", "300", "150", "0.0040")
	cardUsageFile(t, filepath.Join(root, "batch-c", "jobs", "j3", "usage.tsv"),
		"deepseek", "other-model", "2026-09-12T10:00:00Z", "999", "999", "9.9999")

	first := invoke(t, "sum", "--swarm-root", root, "--day", "2026-09-11", "--out", ledger)
	wantExit(t, first, 0)
	wantContains(t, first.stdout, "SUM OK day=2026-09-11 models=1 cards=2 in=1300 out=350 usd=0.0140")

	before := read(t, ledger)

	second := invoke(t, "sum", "--swarm-root", root, "--day", "2026-09-11", "--out", ledger)
	wantExit(t, second, 0)
	after := read(t, ledger)

	if before != after {
		t.Errorf("a second run changed the ledger; the day's rows must be replaced, never doubled:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	lines := strings.Split(strings.TrimRight(after, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("ledger has %d lines, want header + one day row:\n%s", len(lines), after)
	}
	if !strings.Contains(lines[1], "\tdeepseek-v4\t1300\t350\t0.0140\t2") {
		t.Errorf("day row is wrong: %q", lines[1])
	}
	if strings.Contains(after, "other-model") {
		t.Errorf("a card from another day leaked into the ledger:\n%s", after)
	}
}
