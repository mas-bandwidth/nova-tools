package main

import (
	"path/filepath"
	"strings"
	"testing"
)

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
	if first.stdout != second.stdout {
		t.Errorf("a second run printed different lines; the verb is a pure fold:\nfirst:\n%s\nsecond:\n%s", first.stdout, second.stdout)
	}
}
