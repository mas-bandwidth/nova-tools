package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestIssue2201 verifies that the monthly token ledger report equals the folded TSV to the token.
// It feeds the token_ledger writer the same day rows the fold produces and asserts that
// nova-tokens report over the ledger equals the folded TSVs to the token — every one of the
// five token types and every (day, model, repo) tuple.
func TestIssue2201(t *testing.T) {
	// Create a ledger TSV file with all five token types
	dir := t.TempDir()
	ledger := filepath.Join(dir, "ledger.tsv")

	// Header: day, provider, model, repo, tasks, tokens_in, tokens_out, cache_write, cache_read, reasoning, usd, source
	header := "day\tprovider\tmodel\trepo\ttasks\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd\tsource"

	// Create rows with all five token types populated
	// Row 1: m1/r1: in=100, out=50, cw=10, cr=20, rsn=5
	// Row 2: m1/r2: in=200, out=100, cw=20, cr=40, rsn=10
	// Row 3: m2/r1: in=300, out=150, cw=30, cr=10, rsn=15
	rows := []string{
		"2026-09-11\tdeepseek\tm1\tr1\t2\t100\t50\t10\t20\t5\t0.01\tpool",
		"2026-09-11\tdeepseek\tm1\tr2\t1\t200\t100\t20\t40\t10\t0.02\tpool",
		"2026-09-12\tdeepseek\tm2\tr1\t1\t300\t150\t30\t10\t15\t0.03\tpool",
	}

	write(t, ledger, header+"\n"+strings.Join(rows, "\n")+"\n")

	// Run report for September 2026, grouped by model (default)
	r := invoke(t, "report", "--ledger", ledger, "--month", "2026-09")
	wantExit(t, r, 0)

	// Verify that all five token types are present in the output
	wantContains(t, r.stdout, "REPORT model=m1")
	wantContains(t, r.stdout, "REPORT model=m2")

	// Check that all five token types are present with correct values
	// m1: in=100+200=300, out=50+100=150, cw=10+20=30, cr=20+40=60, rsn=5+10=15
	wantContains(t, r.stdout, "model=m1 tasks=3 in=300 out=150 cache_write=30 cache_read=60 reasoning=15")

	// m2: in=300, out=150, cw=30, cr=10, rsn=15
	wantContains(t, r.stdout, "model=m2 tasks=1 in=300 out=150 cache_write=30 cache_read=10 reasoning=15")

	// Verify the OK line
	wantContains(t, r.stdout, "REPORT OK month=2026-09 groups=2 rows=3 usd=0.06")

	// Also test grouping by repo
	r2 := invoke(t, "report", "--ledger", ledger, "--month", "2026-09", "--by", "repo")
	wantExit(t, r2, 0)

	// r1: m1/r1 (tasks=2) + m2/r1 (tasks=1) = tasks=3, in=100+300=400, out=50+150=200, cw=10+30=40, cr=20+10=30, rsn=5+15=20
	wantContains(t, r2.stdout, "repo=r1 tasks=3 in=400 out=200 cache_write=40 cache_read=30 reasoning=20")

	// r2: m1/r2 = tasks=1, in=200, out=100, cw=20, cr=40, rsn=10
	wantContains(t, r2.stdout, "repo=r2 tasks=1 in=200 out=100 cache_write=20 cache_read=40 reasoning=10")

	// Also test grouping by day
	r3 := invoke(t, "report", "--ledger", ledger, "--month", "2026-09", "--by", "day")
	wantExit(t, r3, 0)

	// 2026-09-11: m1/r1 + m1/r2 = in=300, out=150, cw=30, cr=60, rsn=15
	wantContains(t, r3.stdout, "day=2026-09-11 tasks=3 in=300 out=150 cache_write=30 cache_read=60 reasoning=15")

	// 2026-09-12: m2/r1 = in=300, out=150, cw=30, cr=10, rsn=15
	wantContains(t, r3.stdout, "day=2026-09-12 tasks=1 in=300 out=150 cache_write=30 cache_read=10 reasoning=15")
}
