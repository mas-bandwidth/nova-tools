package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestAvgLinesPerModelAndAll pins the two lines the day report adds after its body:
// one TOKENS AVG per model, sorted by usd_per_mtok descending, and one TOKENS AVG-ALL
// summing every model. A model whose tokens sum to zero still prints one line, with
// usd_per_mtok=-, never a division.
func TestAvgLinesPerModelAndAll(t *testing.T) {
	dir := t.TempDir()
	repos := reposFile(t, dir)
	pool := mkdir(t, filepath.Join(dir, "pool"))
	swarmUsage(t, pool, "j1", swarmRowCost("j1", "1", "-", "deepseek", "deepseek-v3", "schema", "2026-09-11T10:00:00Z", "100", "100", "-", "-", "-", "0.02"))
	swarmUsage(t, pool, "j2", swarmRowCost("j2", "1", "-", "openai", "gpt-4o", "schema", "2026-09-11T11:00:00Z", "1000", "200", "-", "-", "-", "0.012345"))
	swarmUsage(t, pool, "j3", swarmRowCost("j3", "1", "-", "x", "zero", "schema", "2026-09-11T12:00:00Z", "0", "-", "-", "-", "-", "0"))

	r := invoke(t, "report", "--who", "rowan", "--day", "2026-09-11", "--repos", repos, "--swarm", "pool="+pool)
	wantExit(t, r, 0)

	deepseek := lineWith(r.stderr, "deepseek/deepseek-v3")
	wantContains(t, deepseek, "tokens=200")
	wantContains(t, deepseek, "usd=0.02")
	wantContains(t, deepseek, "usd_per_mtok=100.0000")

	gpt := lineWith(r.stderr, "openai/gpt-4o")
	wantContains(t, gpt, "tokens=1200")
	wantContains(t, gpt, "usd=0.012345")
	wantContains(t, gpt, "usd_per_mtok=10.2875")

	zero := lineWith(r.stderr, "x/zero")
	wantContains(t, zero, "tokens=0")
	wantContains(t, zero, "usd_per_mtok=-")

	all := lineWith(r.stderr, "TOKENS AVG-ALL")
	wantContains(t, all, "tokens=1400")
	wantContains(t, all, "usd=0.032345")
	wantContains(t, all, "usd_per_mtok=23.1036")

	// Sorted by usd_per_mtok descending: deepseek (100), gpt (10.2875), zero (no rate, last).
	deepI := indexOfLine(r.stderr, "deepseek/deepseek-v3")
	gptI := indexOfLine(r.stderr, "openai/gpt-4o")
	zeroI := indexOfLine(r.stderr, "x/zero")
	if !(deepI < gptI && gptI < zeroI) {
		t.Errorf("AVG lines are not sorted by usd_per_mtok descending:\n%s", r.stderr)
	}
}

// indexOfLine returns the 0-based index of the first line of s containing sub, or -1.
func indexOfLine(s, sub string) int {
	for i, line := range strings.Split(s, "\n") {
		if strings.Contains(line, sub) {
			return i
		}
	}
	return -1
}
