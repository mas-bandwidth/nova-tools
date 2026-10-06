package friend

import (
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rat(s string) *big.Rat { r, _ := new(big.Rat).SetString(s); return r }

// TestOpencodeLanesDoWhatTheRunnerStopgapsDid holds each behaviour of two friends'
// runner.zsh as one table, so the stopgaps can be retired.
func TestOpencodeLanesDoWhatTheRunnerStopgapsDid(t *testing.T) {
	t.Parallel()

	t.Run("the row's settings are read off the beat", func(t *testing.T) {
		t.Parallel()
		r := ParseLaneRules("FRIEND-BEAT OK bob row_mode=one-shot row_width=12 row_tiers=Flash row_streams=security*,fp-sec* row_token_cap=6000000 row_load_bound=90 row_load_width=3")
		assert.Equal(t, LaneRules{Tiers: []string{"flash"}, Patterns: []string{"security*", "fp-sec*"}, TokenCap: 6000000, LoadBound: 90, LoadWidth: 3}, r)
		assert.Equal(t, LaneRules{}, ParseLaneRules("row_mode=one-shot row_token_cap=-5 row_load_bound=x"))
	})

	t.Run("the card filter", func(t *testing.T) {
		t.Parallel()
		flash := LaneRules{Tiers: []string{"flash"}}
		sec := LaneRules{Patterns: []string{"security*", "fp-sec*", "sec-*"}}
		for _, c := range []struct {
			name             string
			r                LaneRules
			id, stream, tier string
			ok               bool
			whyHas           string
		}{
			{"no rules run anything", LaneRules{}, "a", "x", "pro", true, ""},
			{"flash tier passes", flash, "a", "x", "flash", true, ""},
			{"tier is case-blind", flash, "a", "x", "FLASH", true, ""},
			{"pro tier is out", flash, "a", "x", "pro", false, "works flash only"},
			{"no tier is out", flash, "a", "x", "", false, "tier -"},
			{"stream pattern passes", sec, "a", "security-hardening", "pro", true, ""},
			{"id pattern passes", sec, "fp-sec-3", "other", "pro", true, ""},
			{"neither is out", sec, "a", "other", "pro", false, "stream other, id a"},
			{"both must hold", LaneRules{Tiers: []string{"flash"}, Patterns: []string{"security*"}}, "a", "security", "pro", false, "tier pro"},
		} {
			ok, why := c.r.Decide(c.id, c.stream, c.tier)
			assert.Equal(t, c.ok, ok, c.name)
			assert.Contains(t, why, c.whyHas, c.name)
			if c.ok {
				assert.Empty(t, why, c.name)
			}
		}
	})

	t.Run("a dealt card outside the filter and not started is taken back", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			name                                    string
			passes, started, jobDir, took, wantTake bool
		}{
			{"outside, untouched", false, false, false, false, true},
			{"inside stays", true, false, false, false, false},
			{"started stays", false, true, false, false, false},
			{"a job directory made outside stays", false, false, true, false, false},
			{"taken once only", false, false, false, true, false},
		} {
			assert.Equal(t, c.wantTake, TakeBack(c.passes, c.started, c.jobDir, c.took), c.name)
		}
		assert.Equal(t, []string{"friend", "take", "bob", "c1", "--reason", "tier pro"}, TakeArgv("bob", "c1", "tier pro"))
	})

	t.Run("the job name carries the generation", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			card, epoch string
			gen         int
			want        string
		}{
			{"c1", "15", 1, "c1~15"},
			{"c1", "15", 0, "c1~15"},
			{"c1", "15", 2, "c1~15.g2"},
			{"c1", "", 1, "c1"},
			{"c1", "", 3, "c1.g3"},
		} {
			got := JobName(c.card, c.epoch, c.gen)
			assert.Equal(t, c.want, got)
			if c.epoch != "" {
				id, epoch, gen, ok := ParseJob(got)
				require.True(t, ok)
				assert.Equal(t, c.card, id)
				assert.Equal(t, c.epoch, strconv.Itoa(epoch))
				assert.Equal(t, max(c.gen, 1), gen)
			}
		}
	})

	t.Run("the width is held under load", func(t *testing.T) {
		t.Parallel()
		held := LaneRules{LoadBound: 90, LoadWidth: 3}
		for _, c := range []struct {
			name              string
			width, load, want int
			r                 LaneRules
		}{
			{"under the bound", 12, 90, 12, held},
			{"over the bound", 12, 91, 3, held},
			{"never above the row", 2, 200, 2, held},
			{"no bound never holds", 12, 500, 12, LaneRules{}},
			{"held width is at least one", 12, 200, 1, LaneRules{LoadBound: 10}},
		} {
			assert.Equal(t, c.want, LaneWidth(c.width, c.load, c.r), c.name)
		}
	})

	t.Run("the token cap writes a HOLD naming the cap", func(t *testing.T) {
		t.Parallel()
		tk := Tokens{Input: 4_000_000, CacheRead: 1_500_000, Output: 400_000, Reasoning: 100_000}
		assert.True(t, OverCap(tk, 6_000_000))
		assert.False(t, OverCap(tk, 6_000_001))
		assert.False(t, OverCap(tk, 0), "no cap is never over")
		rep := CapHoldReport("Bob", 6_000_000, tk, 41, "$ git push")
		lines := strings.Split(rep, "\n")
		assert.Equal(t, "Verdict: HOLD", lines[0])
		assert.Equal(t, "Head: none", lines[1])
		assert.Contains(t, rep, "token cap: 6000000 tokens, 41 turns, last step: $ git push")
		assert.Contains(t, rep, "cap of 6000000 tokens")
	})

	t.Run("a provider failure pauses every lane", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct{ name, out, want string }{
			{"402", "ok\nError: 402 Payment Required\n", "Error: 402 Payment Required"},
			{"429", "\x1b[31mError: HTTP 429\x1b[0m", "Error: HTTP 429"},
			{"funds", "Error: insufficient_funds on the account", "Error: insufficient_funds on the account"},
			{"rate limit", "Error: Rate limit reached for mercury", "Error: Rate limit reached for mercury"},
			{"a port number is no 429", "Error: connect 127.0.0.1:14290 refused", ""},
			{"other errors do not pause", "Error: file not found", ""},
			{"a non-error line does not pause", "the card mentions rate limit handling", ""},
		} {
			assert.Equal(t, c.want, ProviderFailure(c.out), c.name)
		}
		dir := t.TempDir()
		p := ProviderPause{Message: "Error: 429", Job: "c1~15", At: time.Date(2026, 10, 6, 1, 2, 3, 0, time.UTC)}
		assert.False(t, Paused(dir))
		wrote, err := p.Pause(dir)
		require.NoError(t, err)
		assert.True(t, wrote)
		assert.True(t, Paused(dir))
		raw, err := os.ReadFile(filepath.Join(dir, PauseFile))
		require.NoError(t, err)
		assert.Equal(t, "2026-10-06T01:02:03Z c1~15: Error: 429\n", string(raw))
		wrote, err = ProviderPause{Message: "later", Job: "c2"}.Pause(dir)
		require.NoError(t, err)
		assert.False(t, wrote, "the first message stands")
		raw2, _ := os.ReadFile(filepath.Join(dir, PauseFile))
		assert.Equal(t, raw, raw2)
		assert.Equal(t, []string{"friend", "down", "bob", "--reason", "provider failure (inception/mercury-2.5): Error: 429"}, p.DownArgv("bob", "inception/mercury-2.5"))
	})

	t.Run("the cost line", func(t *testing.T) {
		t.Parallel()
		price := &Price{Route: "flash-mercury", Input: rat("0.25"), CacheRead: rat("0.025"), CacheWrite: rat("0.25"), Output: rat("0.75"), ReasoningAsOutput: true}
		tk := Tokens{Input: 1_000_000, CacheRead: 2_000_000, Output: 300_000, Reasoning: 100_000, Harness: 1.5}
		for _, c := range []struct {
			name string
			tk   Tokens
			p    *Price
			want string
		}{
			{"priced", tk, price, "$0.60"}, // (1M*.25 + 2M*.025 + 400k*.75)/1M = .25+.05+.30
			{"zero tokens", Tokens{}, price, "$0.00"},
			{"one token rounds up to a cent", Tokens{Input: 1}, price, "$0.01"},
			{"exact cent stays", Tokens{Input: 4_000_000}, price, "$1.00"},
			{"no row", tk, nil, "unpriced (no route row for inception/mercury-2.5 in nova-sprint routes)"},
			{"extra prices", tk, &Price{Route: "r", Extra: true, Input: rat("1")}, "unpriced (route r has long-context"},
			{"no cache price", Tokens{CacheRead: 5}, &Price{Route: "r", Input: rat("1")}, "unpriced (route r has no cache_read price)"},
			{"no output price counts reasoning", Tokens{Reasoning: 5}, &Price{Route: "r", ReasoningAsOutput: true, Input: rat("1")}, "unpriced (route r has no output price)"},
			{"reasoning not output is free", Tokens{Reasoning: 5}, &Price{Route: "r", Input: rat("1")}, "$0.00"},
		} {
			got := CostOf(c.tk, c.p, "inception/mercury-2.5")
			assert.True(t, strings.HasPrefix(got, c.want), "%s: got %q want %q", c.name, got, c.want)
		}
		line := CostLine(tk, "$0.60", "inception/mercury-2.5", "flash-mercury")
		assert.Equal(t, "Cost: $0.60 (opencode: $1.50) tokens input=1000000 cache_read=2000000 cache_write=0 output=300000 reasoning=100000 model=inception/mercury-2.5 harness=opencode price_route=flash-mercury", line)
		assert.Contains(t, CostLine(tk, "unpriced (x)", "m", ""), "price_route=-")

		// published under Head:, once
		rep := "Verdict: LAND\nHead: " + strings.Repeat("a", 40) + "\n\nGate lines.\n"
		got := WithCost(rep, line)
		assert.Equal(t, []string{"Verdict: LAND", "Head: " + strings.Repeat("a", 40), line, "", "Gate lines."}, strings.Split(strings.TrimRight(got, "\n"), "\n"))
		assert.Equal(t, got, WithCost(got, "Cost: other"))
		assert.True(t, strings.HasSuffix(WithCost("Verdict: HOLD\n", line), line+"\n"), "no Head: line: at the end")
		res := ResultLines(tk, "$0.60", "m")
		assert.Contains(t, res, "tokens: input=1000000 cache_read=2000000")
		assert.Contains(t, res, "cost: $0.60 (opencode: $1.50)")
	})

	t.Run("the tokens are read from opencode's database", func(t *testing.T) {
		t.Parallel()
		q := TokensSQL("Bob one-shot c'1 99")
		assert.Contains(t, q, "title='Bob one-shot c''1 99'")
		assert.Contains(t, q, "parent_id in (select id from s)")
		tk, err := ParseTokens("10|20|30|40|50|0.5|2\n")
		require.NoError(t, err)
		assert.Equal(t, Tokens{10, 20, 30, 40, 50, 0.5, 2}, tk)
		assert.EqualValues(t, 150, tk.Total())
		for _, bad := range []string{"", "1|2", "a|2|3|4|5|0|1", "1|2|3|4|5|x|1"} {
			_, err := ParseTokens(bad)
			assert.Error(t, err, bad)
		}
	})

	t.Run("the go shims refuse", func(t *testing.T) {
		t.Parallel()
		dir, err := WriteShims(t.TempDir())
		require.NoError(t, err)
		for _, name := range []string{"go", "gofmt"} {
			fi, err := os.Stat(filepath.Join(dir, name))
			require.NoError(t, err)
			assert.NotZero(t, fi.Mode()&0o111, name+" is executable")
			raw, _ := os.ReadFile(filepath.Join(dir, name))
			assert.Contains(t, string(raw), "exit 126")
			assert.Contains(t, string(raw), GoRefusal)
		}
		env := LaneEnv([]string{"A=1", "PATH=/usr/bin", "GOROOT=/usr/local/go"}, dir)
		assert.Equal(t, []string{"A=1", "PATH=" + dir + ":/usr/bin", "GOROOT=/GO-NEVER-RUNS-HERE"}, env)
		assert.Equal(t, []string{"PATH=" + dir, "GOROOT=/GO-NEVER-RUNS-HERE"}, LaneEnv(nil, dir))
	})

	t.Run("a bus note at each finish", func(t *testing.T) {
		t.Parallel()
		s, b := FinishNote("Bob", "c1~15", "Verdict: LAND", "$0.60", 95*time.Second+400*time.Millisecond)
		assert.Equal(t, "Bob card c1~15: Verdict: LAND", s)
		assert.Equal(t, "Bob one-shot lane finished c1~15: Verdict: LAND; cost $0.60; wall 1m35s", b)
		s, _ = FinishNote("Bob", "c1", "", "$0.00", 0)
		assert.Equal(t, "Bob card c1: no report", s)
	})
}
