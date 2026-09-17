package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func writePoolLedgerFixture(t *testing.T, path string) {
	t.Helper()
	header := "day\tprovider\tmodel\trepo\ttasks\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd\tsource"
	rows := []string{
		"2026-09-11\tdeepseek\tm1\tr1\t2\t300\t150\t30\t60\t15\t0.03\tpool",
		"2026-09-12\tdeepseek\tm1\tr1\t1\t100\t50\t10\t20\t5\t0.01\tpool",
		"2026-09-11\tdeepseek\tm2\tr1\t1\t500\t250\t50\t200\t25\t0.05\tpool",
		"2026-10-01\tdeepseek\tm1\tr1\t1\t999\t999\t99\t999\t99\t9.99\tpool",
	}
	write(t, path, header+"\n"+strings.Join(rows, "\n")+"\n")
}

func TestReportLedgerMonthFiltersAndSums(t *testing.T) {
	dir := t.TempDir()
	ledger := filepath.Join(dir, "ledger.tsv")
	writePoolLedgerFixture(t, ledger)

	r := invoke(t, "report", "--ledger", ledger, "--month", "2026-09")
	wantExit(t, r, 0)
	// Only the September rows count: m1 sums two rows, m2 one row, October excluded.
	wantContains(t, r.stdout, "REPORT model=m1 tasks=3 in=400 out=200 cache_read=80 usd=0.04")
	wantContains(t, r.stdout, "REPORT model=m2 tasks=1 in=500 out=250 cache_read=200 usd=0.05")
	wantNotContains(t, r.stdout, "999")
	// Sorted by cache_read descending: m2 (200) before m1 (80).
	if strings.Index(r.stdout, "model=m2") > strings.Index(r.stdout, "model=m1") {
		t.Errorf("want m2 before m1, sorted by cache_read descending:\n%s", r.stdout)
	}
	wantContains(t, r.stdout, "REPORT OK month=2026-09 groups=2 rows=3 usd=0.09")
}

func TestReportLedgerByRepo(t *testing.T) {
	dir := t.TempDir()
	ledger := filepath.Join(dir, "ledger.tsv")
	header := "day\tprovider\tmodel\trepo\ttasks\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd\tsource"
	rows := []string{
		"2026-09-11\tdeepseek\tm1\tr1\t1\t100\t50\t10\t60\t5\t0.01\tpool",
		"2026-09-11\tdeepseek\tm1\tr2\t1\t200\t100\t20\t40\t10\t0.02\tpool",
		"2026-09-11\tdeepseek\tm2\tr1\t1\t300\t150\t30\t10\t15\t-\tpool",
	}
	write(t, ledger, header+"\n"+strings.Join(rows, "\n")+"\n")

	r := invoke(t, "report", "--ledger", ledger, "--month", "2026-09", "--by", "repo")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "REPORT repo=r1 tasks=2 in=400 out=200 cache_read=70 usd=-")
	wantContains(t, r.stdout, "REPORT repo=r2 tasks=1 in=200 out=100 cache_read=40 usd=0.02")
	wantContains(t, r.stdout, "REPORT OK month=2026-09 groups=2 rows=3 usd=-")
}
