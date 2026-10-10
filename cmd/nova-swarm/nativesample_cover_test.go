package main

import (
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// THE LIVE SAMPLER'S UNIT TIER. The unit tier's per-function coverage table held six
// functions of nativesample.go at 0.0% -- readOnce, watching, label, enter, leave and
// Counts -- and this file covers each of them, main path and refusal, with no sleep, no
// real time, no network, no subprocess and no store: the sampler is built at interval 0,
// so its loop never starts, and readOnce runs synchronously against a data home that
// either holds no store (an answered absence, SPEC-SWARM rule 13d's "nothing observed")
// or whose store path cannot be stat'd (a read that FAILS), both endings reached before
// the usage reader ever asks for its `sqlite3`. A sample through a real database is the
// functional tier's (native_budget_sample_test.go), which reaches the same lines with one.

// TestNativesampleCoverReadOnce holds readOnce's two endings at the unit, beside the card
// budget's turn count: an answered read of a data home that holds no store is an absence
// the budget cannot fire on, a read that FAILS is counted with the reason it gave (rule
// 13d: "a read still unanswered at its limit is abandoned and counted as a failed read"),
// and a card budget's turns are counted from the named log before the fold. Every row
// brackets its read with enter and leave, so no two reads are ever in flight together.
func TestNativesampleCoverReadOnce(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		cfg           nativeRunConfig
		log           string
		blockTheStore bool
		wantSamples   int
		wantFailures  int
		wantErrPart   string
	}{
		{
			// A data home whose standard locations hold no database: the read answers
			// with an absence, the figures move by nothing, and no budget fires.
			name:        "an_answered_read_of_a_data_home_with_no_store",
			cfg:         nativeRunConfig{tokens: 1000},
			wantSamples: 1,
		},
		{
			// A regular file where the store's directory would be: the stat of the
			// database beneath it is an error that is not an absence, so the read
			// FAILS and is counted, before any reader is looked for.
			name:          "a_read_that_fails_is_counted_with_its_reason",
			cfg:           nativeRunConfig{tokens: 1000},
			blockTheStore: true,
			wantSamples:   1,
			wantFailures:  1,
			wantErrPart:   "could not be read",
		},
		{
			// A card budget asks its turn count of the capture even where the usage
			// source has reported nothing yet, and the fold fires nothing off an
			// unobserved read.
			name:        "a_card_budgets_turns_are_counted_from_the_named_log",
			cfg:         nativeRunConfig{worker: &swarm.Worker{MaxTurns: 1}},
			log:         "assistant turn one\nassistant turn two\n",
			wantSamples: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dataHome := t.TempDir()
			if tc.blockTheStore {
				require.NoError(t, os.WriteFile(filepath.Join(dataHome, "opencode"), []byte("a file\n"), 0o644))
			}
			logPath := ""
			if tc.log != "" {
				logPath = filepath.Join(dataHome, "harness-output.log")
				require.NoError(t, os.WriteFile(logPath, []byte(tc.log), 0o644))
			}
			s := startLiveSampler(dataHome, 0, tc.cfg, logPath)
			defer s.Stop()
			s.readOnce()

			_, observed, _, failures, lastErr := s.Observed()
			assert.Equal(t, tc.wantFailures, failures, "a failed read is counted as one")
			assert.False(t, observed, "a data home that reports nothing is an absence, never a figure")
			if tc.wantErrPart != "" {
				assert.ErrorContains(t, lastErr, tc.wantErrPart, "a failed read carries the reason it gave")
				assert.Equal(t, lastErr.Error(), s.Why(), "Why is the last failed read's reason, in its own words, for the NATIVE BUDGET line")
			} else {
				assert.NoError(t, lastErr, "an answered read carries no error")
				assert.Empty(t, s.Why(), "an answered read leaves nothing for the line to carry")
			}
			assert.Empty(t, s.StopWordAtFinal(0, false, ""), "a sample that saw nothing fires no budget")
		})
	}
}

// TestNativesampleCoverWatching holds the promise a sampler keeps, by budget: a token
// budget, a dollar budget or a card budget is watched, `unmetered` keeps no promise
// whatever the token budget beside it says, and a sampler with no budget at all watches
// nothing, so a reader that stops answering it ends no card.
func TestNativesampleCoverWatching(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		cfg  nativeRunConfig
		want bool
	}{
		{name: "a_token_budget_is_watched", cfg: nativeRunConfig{tokens: 1000}, want: true},
		{name: "a_dollar_budget_is_watched", cfg: nativeRunConfig{usd: big.NewRat(1, 2)}, want: true},
		{name: "a_card_budget_is_watched", cfg: nativeRunConfig{worker: &swarm.Worker{MaxTurns: 10}}, want: true},
		{name: "a_cache_read_budget_is_watched", cfg: nativeRunConfig{worker: &swarm.Worker{MaxCacheRead: 1000}}, want: true},
		{name: "unmetered_keeps_no_promise", cfg: nativeRunConfig{unmetered: true, tokens: 1000}, want: false},
		{name: "no_budget_at_all_watches_nothing", cfg: nativeRunConfig{}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := startLiveSampler("", 0, tc.cfg, "")
			defer s.Stop()
			assert.Equal(t, tc.want, s.watching(), tc.name)
		})
	}
}

// TestNativesampleCoverLabel holds the id the PROMPT-DEFECT line names: the card's label,
// the same token the NATIVE OK line carries one line above it, and a dash where the
// caller gave none.
func TestNativesampleCoverLabel(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		label string
		want  string
	}{
		{name: "the_cards_label_is_the_id", label: "card-1", want: "card-1"},
		{name: "no_label_prints_the_dash", label: "", want: "-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := startLiveSampler("", 0, nativeRunConfig{label: tc.label}, "")
			defer s.Stop()
			assert.Equal(t, tc.want, s.label(), tc.name)
		})
	}
}
