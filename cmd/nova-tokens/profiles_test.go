package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
)

// cardUsageHeader is the thirteen columns of one card's usage.tsv, transcribed from the
// contract named in internal/swarm/usagecard.go (job, attempt, started, ended, rc, provider,
// model, and the five token types plus usd) and written here from that text, never from a
// constant, so the fixture can disagree with the reader it is meant to check.
const cardUsageHeader = "job\tattempt\tstarted\tended\trc\tprovider\tmodel\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd"

// The issue's sentence: a bounded contract review produced a valid report but overshot its
// accounting budget, so the verb counts, per model, the cards, the median output tokens and
// the overshoot (cards whose output exceeded the card's own budget line when one is present).
// A budget the card does not carry (or a usage row with no output) is an absent line that
// never invents an overshoot, and the verb is a pure fold: a second run over the same root
// prints the same lines, the sequence a friend runs.
func TestSwarmProfilesMeasureExplicitReasoningEffortAndBudgetOvershootOnRealWork(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name  string
		cards [][]string // job dir, budget ("" writes no PROMPT.md), model, started, in, out, usd
		want  []string
	}{
		{"budget overshoot per model", [][]string{
			// deepseek-v4: three cards with budgets, outs 100, 300, 600 -> median 300, one overshoot.
			{"batch-a/jobs/j1", "500", "deepseek-v4", "2026-09-15T10:00:00Z", "1000", "100", "0.0100"},
			{"batch-b/jobs/j2", "500", "deepseek-v4", "2026-09-15T11:00:00Z", "300", "300", "0.0040"},
			{"batch-c/jobs/j3", "500", "deepseek-v4", "2026-09-15T12:00:00Z", "900", "600", "0.0200"},
			// other-model: one card with an unmetered budget (no readable line), so no overshoot.
			{"batch-d/jobs/j4", "unmetered", "other-model", "2026-09-15T13:00:00Z", "999", "999", "9.9999"},
		}, []string{
			"PROFILES MODEL model=deepseek-v4 cards=3 median_out=300 overshoot=1",
			"PROFILES MODEL model=other-model cards=1 median_out=999 overshoot=0",
			"PROFILES OK models=2 cards=4 overshoot=1",
		}},
		{"round trip: no output and no budget is never an overshoot", [][]string{
			{"batch-a/jobs/j1", "200", "deepseek-v4", "2026-09-15T10:00:00Z", "1000", "100", "0.0100"},
			{"batch-b/jobs/j2", "", "deepseek-v4", "2026-09-15T11:00:00Z", "100", "-", "-"},
		}, []string{"PROFILES MODEL model=deepseek-v4 cards=2 median_out=100 overshoot=0"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for _, c := range row.cards {
				job := filepath.Join(root, filepath.FromSlash(c[0]))
				if c[1] != "" { // the one budget line this measurement reads
					testkit.WriteFile(t, filepath.Join(job, "PROMPT.md"), "YOUR DEADLINE IS 600 SECONDS from the start of this run.\n"+
						"YOUR TOKEN BUDGET IS "+c[1]+". The machinery ends the job at the budget it can see.\n")
				}
				testkit.WriteFile(t, filepath.Join(job, "usage.tsv"), cardUsageHeader+"\n"+
					strings.Join([]string{"c", "1", c[3], c[3], "0", "deepseek", c[2], c[4], c[5], "-", "-", "-", c[6]}, "\t")+"\n")
			}
			first := novaTokens.Do(t, "profiles", "--swarm-root", root).Exit(0).Out(row.want...)
			assert.Equal(t, first.Stdout, novaTokens.Do(t, "profiles", "--swarm-root", root).Exit(0).Stdout, "a second run printed different lines")
		})
	}
}
