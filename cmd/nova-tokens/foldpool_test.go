package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// poolHeader is transcribed from SPEC-SWARM rule 12 via tokens.SwarmColumns,
// never copied from the constant, so the fixture can disagree with the reader.
const poolHeader = "job\tattempt\tfrom\tstarted\tended\tend\trc\tprovider\tmodel\trepo\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd"

func poolRow(job, started, provider, model, repo, in, out, cw, cr, rsn, usd string) string {
	return strings.Join([]string{
		job, "1", "swarm", started, started, "done", "0", provider,
		model, repo, in, out, cw, cr, rsn, usd,
	}, "\t")
}

func writePoolFile(t *testing.T, path string, rows ...string) {
	t.Helper()
	write(t, path, poolHeader+"\n"+strings.Join(rows, "\n")+"\n")
}

func TestFoldPoolFourRows(t *testing.T) {
	dir := t.TempDir()
	pool := mkdir(t, filepath.Join(dir, "pool", "usage"))
	ledger := filepath.Join(dir, "ledger.tsv")

	writePoolFile(t, filepath.Join(pool, "a.tsv"),
		poolRow("t1", "2026-09-11T10:00:00Z", "deepseek", "m1", "r1", "100", "50", "10", "20", "5", "0.010000"),
		poolRow("t2", "2026-09-11T11:00:00Z", "deepseek", "m1", "r1", "200", "100", "20", "40", "10", "0.020000"),
	)
	writePoolFile(t, filepath.Join(pool, "b.tsv"),
		poolRow("t3", "2026-09-11T12:00:00Z", "deepseek", "m2", "r1", "300", "150", "30", "60", "15", "0.030000"),
	)
	writePoolFile(t, filepath.Join(pool, "c.tsv"),
		poolRow("t4", "2026-09-12T10:00:00Z", "deepseek", "m1", "r1", "400", "200", "40", "80", "20", "0.040000"),
		poolRow("t5", "2026-09-12T11:00:00Z", "deepseek", "m1", "r2", "500", "250", "50", "100", "25", "0.050000"),
	)

	r := invoke(t, "fold-pool", "--pool", filepath.Join(dir, "pool"), "--ledger", ledger)
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "FOLD OK rows=4 tasks=5 days=2 ledger=")
	body := read(t, ledger)
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("ledger has %d lines, want header + 4 rows:\n%s", len(lines), body)
	}
	if want := "day\tprovider\tmodel\trepo\ttasks\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd\tsource"; lines[0] != want {
		t.Errorf("header is %q, want %q", lines[0], want)
	}
	if want := "2026-09-11\tdeepseek\tm1\tr1\t2\t300\t150\t30\t60\t15\t0.03\tpool"; lines[1] != want {
		t.Errorf("row1 is %q, want %q", lines[1], want)
	}
	if want := "2026-09-11\tdeepseek\tm2\tr1\t1\t300\t150\t30\t60\t15\t0.03\tpool"; lines[2] != want {
		t.Errorf("row2 is %q, want %q", lines[2], want)
	}
	if want := "2026-09-12\tdeepseek\tm1\tr1\t1\t400\t200\t40\t80\t20\t0.04\tpool"; lines[3] != want {
		t.Errorf("row3 is %q, want %q", lines[3], want)
	}
	if want := "2026-09-12\tdeepseek\tm1\tr2\t1\t500\t250\t50\t100\t25\t0.05\tpool"; lines[4] != want {
		t.Errorf("row4 is %q, want %q", lines[4], want)
	}
}

func TestFoldPoolIdempotent(t *testing.T) {
	dir := t.TempDir()
	pool := mkdir(t, filepath.Join(dir, "pool", "usage"))
	ledger := filepath.Join(dir, "ledger.tsv")

	writePoolFile(t, filepath.Join(pool, "a.tsv"),
		poolRow("t1", "2026-09-11T10:00:00Z", "deepseek", "m1", "r1", "100", "50", "10", "20", "5", "0.010000"),
	)
	writePoolFile(t, filepath.Join(pool, "b.tsv"),
		poolRow("t2", "2026-09-11T12:00:00Z", "deepseek", "m2", "r1", "300", "150", "30", "60", "15", "0.030000"),
	)
	writePoolFile(t, filepath.Join(pool, "c.tsv"),
		poolRow("t3", "2026-09-12T10:00:00Z", "deepseek", "m1", "r1", "400", "200", "40", "80", "20", "0.040000"),
	)

	first := invoke(t, "fold-pool", "--pool", filepath.Join(dir, "pool"), "--ledger", ledger)
	wantExit(t, first, 0)
	before := read(t, ledger)
	second := invoke(t, "fold-pool", "--pool", filepath.Join(dir, "pool"), "--ledger", ledger)
	wantExit(t, second, 0)
	after := read(t, ledger)
	if before != after {
		t.Errorf("a second run changed the ledger; upsert must replace the same key:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestFoldPoolDashUsd(t *testing.T) {
	dir := t.TempDir()
	pool := mkdir(t, filepath.Join(dir, "pool", "usage"))
	ledger := filepath.Join(dir, "ledger.tsv")

	writePoolFile(t, filepath.Join(pool, "a.tsv"),
		poolRow("t1", "2026-09-11T10:00:00Z", "deepseek", "m1", "r1", "100", "50", "10", "20", "5", "-"),
		poolRow("t2", "2026-09-11T11:00:00Z", "deepseek", "m1", "r1", "200", "100", "20", "40", "10", "0.020000"),
	)
	writePoolFile(t, filepath.Join(pool, "b.tsv"),
		poolRow("t3", "2026-09-11T12:00:00Z", "deepseek", "m2", "r1", "300", "150", "30", "60", "15", "0.030000"),
	)
	writePoolFile(t, filepath.Join(pool, "c.tsv"),
		poolRow("t4", "2026-09-12T10:00:00Z", "deepseek", "m1", "r1", "400", "200", "40", "80", "20", "0.040000"),
	)

	r := invoke(t, "fold-pool", "--pool", filepath.Join(dir, "pool"), "--ledger", ledger)
	wantExit(t, r, 0)
	body := read(t, ledger)
	line := ""
	for _, l := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if strings.HasPrefix(l, "2026-09-11\tdeepseek\tm1\tr1\t") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("missing folded row:\n%s", body)
	}
	if want := "2026-09-11\tdeepseek\tm1\tr1\t2\t300\t150\t30\t60\t15\t-\tpool"; line != want {
		t.Errorf("dash-usd row is %q, want %q -- any unknown usd input prints -", line, want)
	}
}
