package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// cardUsageFileRepo writes one card's usage.tsv whose receipt names a repo, so the reader
// can key the ledger by (model, repo) and count rc=0 completions from the receipt.
func cardUsageFileRepo(t *testing.T, path, model, repo, rc, started, in, out, usd string) string {
	t.Helper()
	row := strings.Join([]string{"c", "1", started, started, rc, "deepseek", model,
		in, out, "-", "-", "-", usd, repo}, "\t")
	return write(t, path, cardUsageHeader+"\trepo\n"+row+"\n")
}

// TestSumSwarmRootCarriesCostPerCompletedTask is nova-tools #64 / SPEC-TOKENS demanded test
// 32. The routing metric is cost per completed task per (model, repo), so sum --swarm-root
// writes one ledger row per (model, repo) pair with `repo` from the receipt, `completed`
// counting only rc=0 receipts, and `usd_per_task` equal to `usd / completed` to six
// decimals; a pair with completed=0 writes `usd_per_task=-` and never divides, and a
// receipt whose rc is `-` counts in cards and dashes and never in completed. The header is
// the ten columns in order.
func TestSumSwarmRootCarriesCostPerCompletedTask(t *testing.T) {
	dir := t.TempDir()
	root := mkdir(t, filepath.Join(dir, "root"))
	ledger := filepath.Join(dir, "ledger.tsv")

	// schema: two finished cards plus one whose rc names nothing, so completed counts two
	// and that one card is a dash in the rc position of the dashes tuple.
	cardUsageFileRepo(t, filepath.Join(root, "b1", "jobs", "j1", "usage.tsv"), "deepseek-v4", "schema", "0", "2026-09-11T10:00:00Z", "100", "10", "0.0200")
	cardUsageFileRepo(t, filepath.Join(root, "b2", "jobs", "j2", "usage.tsv"), "deepseek-v4", "schema", "0", "2026-09-11T11:00:00Z", "300", "30", "0.0400")
	cardUsageFileRepo(t, filepath.Join(root, "b3", "jobs", "j3", "usage.tsv"), "deepseek-v4", "schema", "-", "2026-09-11T12:00:00Z", "70", "7", "0.0050")
	// other: no rc=0 card, so completed is zero and usd_per_task is a dash, never a division.
	cardUsageFileRepo(t, filepath.Join(root, "b4", "jobs", "j4", "usage.tsv"), "deepseek-v4", "other", "1", "2026-09-11T13:00:00Z", "50", "5", "0.0100")

	r := invoke(t, "sum", "--swarm-root", root, "--day", "2026-09-11", "--out", ledger)
	wantExit(t, r, 0)

	lines := strings.Split(strings.TrimRight(read(t, ledger), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("ledger has %d lines, want a header and one row per (model, repo) pair:\n%s", len(lines), read(t, ledger))
	}
	if want := "day\tmodel\trepo\ttokens_in\ttokens_out\tusd\tcards\tcompleted\tusd_per_task\tdashes"; lines[0] != want {
		t.Fatalf("ledger header is %q, want the ten columns %q", lines[0], want)
	}
	if want := "2026-09-11\tdeepseek-v4\tother\t50\t5\t0.0100\t1\t0\t-\t0,0,0,0"; lines[1] != want {
		t.Errorf("the completed=0 row is %q, want %q -- usd_per_task is -, never a division", lines[1], want)
	}
	if want := "2026-09-11\tdeepseek-v4\tschema\t470\t47\t0.0650\t3\t2\t0.032500\t0,0,0,1"; lines[2] != want {
		t.Errorf("the schema row is %q, want %q -- completed is the rc=0 count and usd_per_task is usd/completed", lines[2], want)
	}
}

// TestSumSwarmRefusesALedgerNotInTheTenColumnOrder pins the one refusal the shape adds: a
// ledger whose header is the old eight columns is refused (exit 2) naming the ten.
func TestSumSwarmRefusesALedgerNotInTheTenColumnOrder(t *testing.T) {
	dir := t.TempDir()
	root := mkdir(t, filepath.Join(dir, "root"))
	ledger := write(t, filepath.Join(dir, "ledger.tsv"),
		"day\tmodel\ttokens_in\ttokens_out\tusd\tcards\tdashes\n")
	cardUsageFile(t, filepath.Join(root, "b1", "jobs", "j1", "usage.tsv"),
		"deepseek", "deepseek-v4", "2026-09-11T10:00:00Z", "1", "1", "0.0001")

	r := invoke(t, "sum", "--swarm-root", root, "--day", "2026-09-11", "--out", ledger)
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "SUM REFUSED")
	wantContains(t, r.stderr, "repo")
	wantContains(t, r.stderr, "completed")
	wantContains(t, r.stderr, "usd_per_task")
}
