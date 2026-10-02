package main

// What a fold and a report count: messages dropped for want of an id, a swarm job's retried
// attempt, and the AVG lines a day report adds after its body.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Dropping every message leaves nothing to fold, so the run exits 1 with its
// counts. Dropping only some messages is a note: the fold still accounts for
// messages with ids. TestWhatOneRunPrintsAndWrites covers that partial case.
func TestAFoldThatDropsMessagesWithNoID(t *testing.T) {
	t.Parallel()

	t.Run("every message dropped", func(t *testing.T) {
		t.Parallel()
		b := newBench(t)
		b.transcript("a.jsonl", msg("", "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 9000}, "/x/schema/a.go"),
			msg("", "2026-09-11T10:00:01Z", "f", map[string]int{"input_tokens": 1}, "/x/schema/a.go"))
		r := b.fold("--claude", "g="+b.tr).Exit(1).Err("TOKENS FAIL days=0 rows=0").NotOut("TOKENS OK")
		fail := lineWith(r.Stderr, "FOLD FAIL dropped=")
		assert.Contains(t, fail, "FOLD FAIL dropped=2 of 2: no message had an id")
		assert.Contains(t, fail, "run: nova-tokens sources")
		// the note still names the source and does not claim that nothing else says so.
		note := lineWith(r.Stdout, "TOKENS NOTE")
		assert.Contains(t, note, "claude:g")
		assert.NotContains(t, note, "nothing else says so")
	})
	t.Run("some messages dropped stays a note", func(t *testing.T) {
		t.Parallel()
		b := newBench(t)
		b.transcript("a.jsonl", msg("", "2026-09-11T10:00:00Z", "f", map[string]int{"input_tokens": 9000}, "/x/schema/a.go"),
			msg("m1", "2026-09-11T10:00:01Z", "f", map[string]int{"input_tokens": 1}, "/x/schema/a.go"))
		r := b.fold("--claude", "g="+b.tr).Exit(0).NotErr("dropped=")
		assert.Contains(t, lineWith(r.Stdout, "TOKENS NOTE"), "no id")
	})
}

// A failed attempt followed by success retains both costs. SPEC-TOKENS rule 14 says a row for
// a second attempt (attempt=2) is its own row; a reader that deduped by job alone would fold
// a retried attempt as dup=<n>, and its tokens would never reach the day file.
func TestSwarmRetryAttemptIsItsOwnRow(t *testing.T) {
	t.Parallel()

	b := newBench(t)
	pool := filepath.Join(b.dir, "pool")
	swarmUsage(t, pool, "j1", strings.Join([]string{ // the first attempt failed (rc=1)
		"j1", "1", "-", "2026-09-11T10:00:00Z", "2026-09-11T10:05:00Z", "done", "1", "deepseek",
		"deepseek-v3", "serialize", "100", "20", "-", "-", "-", "-",
	}, "\t")+"\n"+strings.Join([]string{ // the retry succeeded (rc=0)
		"j1", "2", "j1", "2026-09-11T11:00:00Z", "2026-09-11T11:05:00Z", "done", "0", "deepseek",
		"deepseek-v3", "serialize", "1000", "200", "-", "-", "-", "-",
	}, "\t"))
	r := b.fold("--swarm", "deepseek="+pool).Exit(0)
	assert.Contains(t, lineWith(r.Stdout, "TOKENS SOURCE"), "dup=0")
	assert.Contains(t, b.day(), "\tdeepseek-v3\tserialize\t1100\t220\t", "the failed attempt and its retry did not both fold")
}

// The two lines the day report adds after its body: one TOKENS AVG per model, sorted by
// usd_per_mtok descending, and one TOKENS AVG-ALL summing every model. A model whose tokens
// sum to zero still prints one line, with usd_per_mtok=-, never a division.
func TestAvgLinesPerModelAndAll(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pool := filepath.Join(dir, "pool")
	swarmUsage(t, pool, "j1", swarmRowCost("j1", "1", "-", "deepseek", "deepseek-v3", "schema", "2026-09-11T10:00:00Z", "100", "100", "-", "-", "-", "0.02"))
	swarmUsage(t, pool, "j2", swarmRowCost("j2", "1", "-", "openai", "gpt-4o", "schema", "2026-09-11T11:00:00Z", "1000", "200", "-", "-", "-", "0.012345"))
	swarmUsage(t, pool, "j3", swarmRowCost("j3", "1", "-", "x", "zero", "schema", "2026-09-11T12:00:00Z", "0", "-", "-", "-", "-", "0"))

	r := novaTokens.Do(t, "report", "--who", "rowan", "--day", "2026-09-11", "--repos", reposFile(t, dir), "--swarm", "pool="+pool).Exit(0)
	for _, want := range [][]string{
		{"deepseek/deepseek-v3", "tokens=200", "usd=0.02", "usd_per_mtok=100.0000"},
		{"openai/gpt-4o", "tokens=1200", "usd=0.012345", "usd_per_mtok=10.2875"},
		{"x/zero", "tokens=0", "usd_per_mtok=-"},
		{"TOKENS AVG-ALL", "tokens=1400", "usd=0.032345", "usd_per_mtok=23.1036"},
	} {
		line := lineWith(r.Stderr, want[0])
		for _, w := range want[1:] {
			assert.Contains(t, line, w, "the line naming %s", want[0])
		}
	}
	// Sorted by usd_per_mtok descending: deepseek (100), gpt (10.2875), zero (no rate, last).
	at := func(model string) int { return strings.Index(r.Stderr, model) }
	assert.Less(t, at("deepseek/deepseek-v3"), at("openai/gpt-4o"), "AVG lines are not sorted by usd_per_mtok descending: %s", r)
	assert.Less(t, at("openai/gpt-4o"), at("x/zero"), "AVG lines are not sorted by usd_per_mtok descending: %s", r)
}
