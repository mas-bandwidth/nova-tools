package main

import (
	"github.com/stretchr/testify/assert"
	"math"
	"path/filepath"
	"strings"
	"testing"
)

// cardUsageHeader is the thirteen columns of one card's usage.tsv, transcribed from the
// contract named in pkg/swarm/usagecard.go (job, attempt, started, ended, rc, provider,
// model, and the five token types plus usd) and written here from that text, never from a
// constant, so the fixture can disagree with the reader it is meant to check.
const cardUsageHeader = "job\tattempt\tstarted\tended\trc\tprovider\tmodel\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd"

// cardUsageFile writes one card's usage.tsv: the header and one row.
func cardUsageFile(t *testing.T, path, provider, model, started, in, out, usd string) string {
	t.Helper()
	row := strings.Join([]string{"c", "1", started, started, "0", provider, model, in, out, "-", "-", "-", usd}, "\t")
	return write(t, path, cardUsageHeader+"\n"+row+"\n")
}

// cardPrompt writes the card's PROMPT.md beside its usage.tsv, holding the one budget line
// this measurement reads: "YOUR TOKEN BUDGET IS <n>." A card whose budget is not a number
// (here, "unmetered") keeps no readable budget line, the same way a fallback usage.tsv has
// no sibling card at all.
func cardPrompt(t *testing.T, jobDir, budget string) string {
	t.Helper()
	return write(t, filepath.Join(jobDir, "PROMPT.md"),
		"YOUR DEADLINE IS 600 SECONDS from the start of this run.\n"+
			"YOUR TOKEN BUDGET IS "+budget+". The machinery ends the job at the budget it can see.\n")
}

// TestSwarmProfilesMeasureExplicitReasoningEffortAndBudgetOvershootOnRealWork holds the
// issue's sentence: a bounded contract review produced a valid report but overshot its
// accounting budget, so the verb counts, per model, the cards, the median output tokens and
// the overshoot (cards whose output exceeded the card's own budget line when one is present).
func TestSwarmProfilesMeasureExplicitReasoningEffortAndBudgetOvershootOnRealWork(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := mkdir(t, filepath.Join(dir, "root"))

	// deepseek-v4: three cards with budgets, outs 100, 300, 600 -> median 300, one overshoot.
	cardPrompt(t, filepath.Join(root, "batch-a", "jobs", "j1"), "500")
	cardUsageFile(t, filepath.Join(root, "batch-a", "jobs", "j1", "usage.tsv"),
		"deepseek", "deepseek-v4", "2026-09-15T10:00:00Z", "1000", "100", "0.0100")
	cardPrompt(t, filepath.Join(root, "batch-b", "jobs", "j2"), "500")
	cardUsageFile(t, filepath.Join(root, "batch-b", "jobs", "j2", "usage.tsv"),
		"deepseek", "deepseek-v4", "2026-09-15T11:00:00Z", "300", "300", "0.0040")
	cardPrompt(t, filepath.Join(root, "batch-c", "jobs", "j3"), "500")
	cardUsageFile(t, filepath.Join(root, "batch-c", "jobs", "j3", "usage.tsv"),
		"deepseek", "deepseek-v4", "2026-09-15T12:00:00Z", "900", "600", "0.0200")

	// other-model: one card with an unmetered budget (no readable line), so no overshoot.
	cardPrompt(t, filepath.Join(root, "batch-d", "jobs", "j4"), "unmetered")
	cardUsageFile(t, filepath.Join(root, "batch-d", "jobs", "j4", "usage.tsv"),
		"deepseek", "other-model", "2026-09-15T13:00:00Z", "999", "999", "9.9999")

	r := invoke(t, "profiles", "--swarm-root", root)
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "PROFILES MODEL model=deepseek-v4 cards=3 median_out=300 overshoot=1")
	wantContains(t, r.stdout, "PROFILES MODEL model=other-model cards=1 median_out=999 overshoot=0")
	wantContains(t, r.stdout, "PROFILES OK models=2 cards=4 overshoot=1")
}

// TestSwarmProfilesRoundTripIs the sequence a friend runs: the verb over a root writes the
// same lines every time, and a budget the card does not carry (or a usage row with no output)
// is an absent line that never invents an overshoot.
func TestSwarmProfilesRoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := mkdir(t, filepath.Join(dir, "root"))

	cardPrompt(t, filepath.Join(root, "batch-a", "jobs", "j1"), "200")
	cardUsageFile(t, filepath.Join(root, "batch-a", "jobs", "j1", "usage.tsv"),
		"deepseek", "deepseek-v4", "2026-09-15T10:00:00Z", "1000", "100", "0.0100")
	// A card with no output token (unknown) and no budget must never count as an overshoot.
	write(t, filepath.Join(root, "batch-b", "jobs", "j2", "usage.tsv"),
		cardUsageHeader+"\n"+strings.Join([]string{"c", "1", "2026-09-15T11:00:00Z", "2026-09-15T11:00:00Z", "0", "deepseek", "deepseek-v4", "100", "-", "-", "-", "-", "-"}, "\t")+"\n")

	first := invoke(t, "profiles", "--swarm-root", root)
	wantExit(t, first, 0)
	wantContains(t, first.stdout, "PROFILES MODEL model=deepseek-v4 cards=2 median_out=100 overshoot=0")

	second := invoke(t, "profiles", "--swarm-root", root)
	wantExit(t, second, 0)
	assert.Equal(t, second.stdout, first.stdout, "a second run printed different lines; the verb is a pure fold:\nfirst:\n%s\nsecond:\n%s", first.stdout, second.stdout)
}

func TestSwarmProfilesRefusesNonexistentRoot(t *testing.T) {
	t.Parallel()

	r := invoke(t, "profiles", "--swarm-root", filepath.Join(t.TempDir(), "nope"))
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "PROFILES REFUSED:")
	wantContains(t, r.stderr, "does not exist")
	wantContains(t, r.stderr, "nope")
}

// TestProfilesTreatsAnOversizeCardFileAsUnreadable: a usage.tsv larger than 1 MiB is skipped
// (cards=0) and a PROMPT.md larger than 4 MiB yields no budget, so overshoot=0.
func TestProfilesTreatsAnOversizeCardFileAsUnreadable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := mkdir(t, filepath.Join(dir, "root"))
	jobDir := filepath.Join(root, "batch-a", "jobs", "j1")
	mkdir(t, jobDir)

	// Write a valid usage.tsv but larger than 1 MiB (1<<20 = 1048576 bytes)
	validRow := "c\t1\t2026-09-15T10:00:00Z\t2026-09-15T10:00:00Z\t0\tdeepseek\tdeepseek-v4\t1000\t500\t-\t-\t-\t0.0100\n"
	validContent := cardUsageHeader + "\n" + validRow
	oversizeUsage := validContent + strings.Repeat("x", 1024*1024-len(validContent)+1) // 1 byte over cap
	write(t, filepath.Join(jobDir, "usage.tsv"), oversizeUsage)

	// Write a valid budget PROMPT.md (under 4 MiB)
	cardPrompt(t, jobDir, "1000")

	r := invoke(t, "profiles", "--swarm-root", root)
	wantExit(t, r, 0)
	// Oversized usage.tsv should be skipped entirely: no model lines, only OK with cards=0
	wantContains(t, r.stdout, "PROFILES OK models=0 cards=0 overshoot=0")

	// A valid usage.tsv beside a PROMPT.md over 4 MiB: the card counts, but the budget is an
	// absence. The budget line leads the file, so an uncapped read would find it and count
	// the 500-token output against a budget of 100 as an overshoot.
	root2 := mkdir(t, filepath.Join(dir, "root2"))
	jobDir2 := filepath.Join(root2, "batch-a", "jobs", "j1")
	mkdir(t, jobDir2)
	write(t, filepath.Join(jobDir2, "usage.tsv"), validContent)
	head := "YOUR TOKEN BUDGET IS 100. The machinery ends the job at the budget it can see.\n"
	write(t, filepath.Join(jobDir2, "PROMPT.md"), head+strings.Repeat("x", 4<<20-len(head)+1)) // 1 byte over cap

	r2 := invoke(t, "profiles", "--swarm-root", root2)
	wantExit(t, r2, 0)
	wantContains(t, r2.stdout, "PROFILES OK models=1 cards=1 overshoot=0")
}

// TestMedianOutDoesNotOverflowOnTwoLargeCounts verifies that medianOut handles overflow correctly
// when computing the median of two large int64 values near MaxInt64.
func TestMedianOutDoesNotOverflowOnTwoLargeCounts(t *testing.T) {
	t.Parallel()

	// Two MaxInt64 values should yield MaxInt64, not -1 from overflow
	assert.Equal(t, "9223372036854775807", medianOut([]int64{math.MaxInt64, math.MaxInt64}))
	// Standard cases
	assert.Equal(t, "1", medianOut([]int64{1, 2}))
	assert.Equal(t, "2", medianOut([]int64{1, 3}))
	assert.Equal(t, "5", medianOut([]int64{5}))
	assert.Equal(t, "-", medianOut([]int64{}))
}
