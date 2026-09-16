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

// TestSumRefusesOutDirectory pins that --out for the swarm-root form is the ledger
// FILE, so an existing directory at --out is refused by name (file vs directory),
// not by the raw OS rename error. Before #496 the tmp-rename leaked "file exists".
func TestSumRefusesOutDirectory(t *testing.T) {
	dir := t.TempDir()
	root := mkdir(t, filepath.Join(dir, "root"))
	out := mkdir(t, filepath.Join(dir, "swarm-sum-out"))

	r := invoke(t, "sum", "--swarm-root", root, "--day", "2026-09-11", "--out", out)
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "SUM REFUSED")
	wantContains(t, r.stderr, "not a directory")
	wantContains(t, r.stderr, "ledger")
	wantNotContains(t, r.stderr, "file exists")
}

// TestSumSwarmAllUnknownModelIsDashNeverZero is #495's other face: a model whose only
// card reported none of the three kept fields — a dash in, an empty out, a malformed usd —
// lands `-`, `-`, `-` with a dashes count of 1,1,1, never the 0, 0, 0.0000 that reads as
// a free route, while a fully reported model on the same day keeps its numbers and a
// dashes count of 0,0,0. The header is pinned byte for byte to the eight columns the
// binary writes and refuses a ledger for, `dashes` last.
func TestSumSwarmAllUnknownModelIsDashNeverZero(t *testing.T) {
	dir := t.TempDir()
	root := mkdir(t, filepath.Join(dir, "root"))
	ledger := filepath.Join(dir, "ledger.tsv")

	cardUsageFile(t, filepath.Join(root, "batch-a", "jobs", "j1", "usage.tsv"),
		"deepseek", "deepseek-v4", "2026-09-11T10:00:00Z", "1000", "200", "0.0100")
	cardUsageFile(t, filepath.Join(root, "batch-b", "jobs", "j2", "usage.tsv"),
		"deepseek", "deepseek-v3", "2026-09-11T11:00:00Z", "-", "", "0.0xy")

	r := invoke(t, "sum", "--swarm-root", root, "--day", "2026-09-11", "--out", ledger)
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "SUM OK day=2026-09-11 models=2 cards=2 in=1000 out=200 usd=0.0100")

	lines := strings.Split(strings.TrimRight(read(t, ledger), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("ledger has %d lines, want header + one row per model:\n%s", len(lines), read(t, ledger))
	}
	if want := "day\tmodel\ttokens_in\ttokens_out\tusd\tcards\tdashes"; lines[0] != want {
		t.Errorf("ledger header is %q, want %q", lines[0], want)
	}
	if want := "2026-09-11\tdeepseek-v3\t-\t-\t-\t1\t1,1,1"; lines[1] != want {
		t.Errorf("the all-unknown model's row is %q, want %q -- a kept field no card reported is -, never a 0 that reads as a free route", lines[1], want)
	}
	if want := "2026-09-11\tdeepseek-v4\t1000\t200\t0.0100\t1\t0,0,0"; lines[2] != want {
		t.Errorf("the fully reported model's row is %q, want %q", lines[2], want)
	}
}

func TestSumSwarmMixedKnownUnknownDailyAggregate(t *testing.T) {
	dir := t.TempDir()
	root := mkdir(t, filepath.Join(dir, "root"))
	ledger := filepath.Join(dir, "ledger.tsv")

	// One card reports real input, output and dollars; the other, same model and same day,
	// reports none of the three — a dash in, an empty out, and a malformed usd — so the
	// day's aggregate must keep the known card's numbers and carry an explicit unknown
	// count for the other, never fold it into a silent 0 that reads as a free route.
	cardUsageFile(t, filepath.Join(root, "batch-a", "jobs", "j1", "usage.tsv"),
		"deepseek", "deepseek-v4", "2026-09-11T10:00:00Z", "1000", "200", "0.0100")
	write(t, filepath.Join(root, "batch-b", "jobs", "j2", "usage.tsv"),
		cardUsageHeader+"\n"+strings.Join([]string{
			"c", "1", "2026-09-11T11:00:00Z", "2026-09-11T11:00:00Z", "0", "deepseek", "deepseek-v4",
			"-", "", "-", "-", "-", "0.0xy",
		}, "\t")+"\n")

	first := invoke(t, "sum", "--swarm-root", root, "--day", "2026-09-11", "--out", ledger)
	wantExit(t, first, 0)

	lines := strings.Split(strings.TrimRight(read(t, ledger), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("ledger has %d lines, want header + one day row:\n%s", len(lines), read(t, ledger))
	}
	if !strings.Contains(lines[0], "\tdashes") {
		t.Errorf("header has no dashes column for the unknown count: %q", lines[0])
	}
	// The known card's numbers survive, and the unknown card is the explicit dashes count
	// 1,1,1 — not a zero in any kept field that would make the model look free.
	if !strings.Contains(lines[1], "\tdeepseek-v4\t1000\t200\t0.0100\t2\t1,1,1") {
		t.Errorf("day row does not carry the known numbers plus an explicit unknown count: %q", lines[1])
	}
}
