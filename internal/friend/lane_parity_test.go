package friend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The two runner.zsh stopgaps retire when the opencode lanes do what
// they did, each behaviour with its own table test below; this one names them all.
func TestOpencodeLanesDoWhatTheRunnerStopgapsDid(t *testing.T) {
	t.Parallel()
	t.Run("filter", TestLaneFilterSortsDealtCards)
	t.Run("take back", TestLaneTakeBackOnlyWhenNotStarted)
	t.Run("job name", TestLaneJobNameCarriesTheGeneration)
	t.Run("width under load", TestLaneWidthIsHeldUnderLoad)
	t.Run("token cap", TestLaneTokenCapWritesAHold)
	t.Run("provider pause", TestLaneProviderFailurePausesUntilAPersonResumes)
	t.Run("cost line", TestLaneCostIsReadPricedAndPublished)
	t.Run("shims", TestLaneShimsRefuseGo)
	t.Run("bus note", TestLaneFinishNote)
}

func TestLaneFilterSortsDealtCards(t *testing.T) {
	t.Parallel()
	flash := LaneRules{Tiers: []string{"flash"}}
	sec := LaneRules{Streams: []string{"security*"}, IDs: []string{"fp-sec*", "sec-*", "security-*"}}
	for _, c := range []struct {
		name  string
		rules LaneRules
		card  CardFacts
		want  FilterAction
	}{
		{"flash friend, flash card", flash, CardFacts{"a", "mech", "flash"}, FilterRun},
		{"flash friend, pro card is taken back", flash, CardFacts{"a", "mech", "pro"}, FilterTake},
		{"flash friend, no tier is taken back", flash, CardFacts{"a", "mech", "-"}, FilterTake},
		{"security by stream", sec, CardFacts{"x", "security-2026", "pro"}, FilterRun},
		{"security by id", sec, CardFacts{"fp-sec-9", "mech", "pro"}, FilterRun},
		{"security by id prefix sec-", sec, CardFacts{"sec-1", "mech", "pro"}, FilterRun},
		{"not security is skipped", sec, CardFacts{"x", "mech", "pro"}, FilterSkip},
		{"no rules runs everything", LaneRules{}, CardFacts{"x", "mech", "heavy"}, FilterRun},
		{"tier checked before stream", LaneRules{Tiers: []string{"flash"}, Streams: []string{"sec*"}}, CardFacts{"x", "mech", "pro"}, FilterTake},
	} {
		got, why := c.rules.Filter(c.card)
		assert.Equal(t, c.want, got, c.name)
		assert.Equal(t, c.want != FilterRun, why != "", c.name+": a refusal says why")
	}
}

func TestLaneTakeBackOnlyWhenNotStarted(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		action  FilterAction
		started bool
		want    bool
	}{
		{"take, not started", FilterTake, false, true},
		{"take, job dir exists", FilterTake, true, false},
		{"skip is never taken", FilterSkip, false, false},
		{"run is never taken", FilterRun, false, false},
	} {
		assert.Equal(t, c.want, TakeBack(c.action, c.started), c.name)
	}
	assert.Equal(t, []string{"friend", "take", "freddy", "c1", "--reason", "tier pro"}, TakeArgv("freddy", "c1", "tier pro"))
}

func TestLaneJobNameCarriesTheGeneration(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		card       string
		epoch, gen int
		want       string
	}{
		{"c", 15, 1, "c~15"},
		{"c", 15, 0, "c~15"},
		{"c", 15, 2, "c~15.g2"},
		{"c", 0, 3, "c~0.g3"},
		{"c", -1, 1, "c"},
		{"c", -1, 2, "c.g2"},
	} {
		got := JobName(c.card, c.epoch, c.gen)
		assert.Equal(t, c.want, got)
		if c.epoch >= 0 {
			id, e, g, ok := ParseJob(got)
			assert.True(t, ok)
			assert.Equal(t, []any{c.card, c.epoch, max(c.gen, 1)}, []any{id, e, g}, "ParseJob reads it back")
		}
	}
}

func TestLaneWidthIsHeldUnderLoad(t *testing.T) {
	t.Parallel()
	r := LaneRules{LoadMax: 90}
	for _, c := range []struct {
		name  string
		rules LaneRules
		width int
		load  float64
		want  int
	}{
		{"load under the bound", r, 12, 40, 12},
		{"load at the bound", r, 12, 90, 12},
		{"load above holds to 3", r, 12, 91, 3},
		{"narrow row stays narrow", r, 2, 200, 2},
		{"configured held width", LaneRules{LoadMax: 50, LoadTo: 5}, 12, 60, 5},
		{"no bound, no hold", LaneRules{}, 12, 500, 12},
	} {
		assert.Equal(t, c.want, c.rules.LoadWidth(c.width, c.load), c.name)
	}
}

func TestLaneTokenCapWritesAHold(t *testing.T) {
	t.Parallel()
	r := LaneRules{TokenCap: 1000}
	for _, c := range []struct {
		name string
		t    Tokens
		want bool
	}{
		{"under", Tokens{Input: 500, Output: 100}, false},
		{"all kinds count", Tokens{Input: 400, CacheRead: 300, CacheWrite: 100, Output: 100, Reasoning: 100}, true},
		{"over", Tokens{Input: 5000}, true},
	} {
		assert.Equal(t, c.want, r.OverCap(c.t), c.name)
	}
	assert.False(t, LaneRules{}.OverCap(Tokens{Input: 1 << 40}), "no cap is no cap")
	rep := CapHoldReport("Freddy", r, Tokens{Input: 1500, Turns: 7}, "$ git push")
	lines := strings.Split(rep, "\n")
	assert.Equal(t, "Verdict: HOLD", lines[0])
	assert.Equal(t, "Head: none", lines[1])
	assert.Contains(t, rep, "per-card cap of 1000 tokens")
	assert.Contains(t, rep, "1500 tokens, 7 turns, last step: $ git push")
}

func TestLaneProviderFailurePausesUntilAPersonResumes(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, out string
		paused    bool
	}{
		{"402", "Error: 402 Payment Required: insufficient balance", true},
		{"out of funds", "Error: out of funds", true},
		{"429", "Error: request failed with status 429, too many requests", true},
		{"rate limit", "Error: rate limit reached for model", true},
		{"ordinary error", "Error: file not found", false},
		{"clean", "done", false},
	} {
		msg := ProviderFailure("job", c.out)
		assert.Equal(t, c.paused, msg != "", c.name)
	}
	dir := t.TempDir()
	assert.Equal(t, "", ReadPause(dir))
	require.NoError(t, WritePause(dir, "c~1", "Error: 402 Payment Required", "t1"))
	require.NoError(t, WritePause(dir, "c~2", "another", "t2"))
	assert.Equal(t, "t1 c~1: Error: 402 Payment Required", ReadPause(dir), "the first message stays, and survives a restart")
	require.NoError(t, os.Remove(filepath.Join(dir, PauseFile)))
	assert.Equal(t, "", ReadPause(dir), "a person removing it resumes")
	assert.Equal(t, []string{"friend", "down", "freddy", "--reason", "provider failure (inception/m): Error: 402"}, DownArgv("freddy", "inception/m", "Error: 402"))
}

const routesJSON = `{"routes":[{"name":"other","provider":"x","model":"y","prices":{"input":"1"}},
{"name":"flash-mercury","provider":"inception","model":"mercury-2.5","prices":{"input":"0.25","cache_read":"0.025","output":"0.75","reasoning_as_output":true}},
{"name":"long","provider":"p","model":"l","prices":{"input":"1","output":"2","long_context":200000}},
{"name":"noout","provider":"p","model":"n","prices":{"input":"1"}}]}`

func TestLaneCostIsReadPricedAndPublished(t *testing.T) {
	t.Parallel()
	row, err := ParseRoutes([]byte(routesJSON), "inception", "mercury-2.5")
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.Equal(t, "flash-mercury", row.Name)
	missing, err := ParseRoutes([]byte(routesJSON), "inception", "nope")
	require.NoError(t, err)
	assert.Nil(t, missing)
	long, _ := ParseRoutes([]byte(routesJSON), "p", "l")
	noout, _ := ParseRoutes([]byte(routesJSON), "p", "n")
	for _, c := range []struct {
		name  string
		row   *RoutePrices
		t     Tokens
		want  string
		cents int64
	}{
		{"no row", nil, Tokens{Input: 1}, "unpriced (no route row for inception/mercury-2.5 in nova-sprint routes)", 0},
		{"zero tokens", row, Tokens{}, "$0.00", 0},
		{"one token rounds up to a cent", row, Tokens{Input: 1}, "$0.01", 1},
		{"exact", row, Tokens{Input: 4_000_000}, "$1.00", 100},
		{"just over rounds up", row, Tokens{Input: 4_000_001}, "$1.01", 101},
		{"reasoning is output", row, Tokens{Output: 1_000_000, Reasoning: 1_000_000}, "$1.50", 150},
		{"cache read priced", row, Tokens{CacheRead: 40_000_000}, "$1.00", 100},
		{"cache write has no price", row, Tokens{CacheWrite: 1}, "unpriced (route flash-mercury has no cache_write price)", 0},
		{"long context not applied", long, Tokens{Input: 1}, "unpriced (route long has long-context, request or gateway prices this pricing does not apply)", 0},
		{"no output price", noout, Tokens{Output: 1}, "unpriced (route noout has no output price)", 0},
	} {
		got := Price(c.row, "inception/mercury-2.5", c.t)
		assert.Equal(t, c.want, got.Dollars(), c.name)
		assert.Equal(t, c.cents, got.Cents, c.name)
	}

	tk, err := ParseTokens("100|20|3|40|5|0.0123|2\n")
	require.NoError(t, err)
	assert.Equal(t, Tokens{100, 20, 3, 40, 5, 0.0123, 2, 0}, tk)
	_, err = ParseTokens("1|2")
	assert.Error(t, err)
	assert.Contains(t, TokensSQL("it's"), "title='it''s'", "the title is quoted")

	var gotArgs []string
	fake := func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
		gotArgs = append([]string{name}, args...)
		return "1|2|3|4|5|0|1\n", 0, nil
	}
	tk, err = ReadTokens(context.Background(), fake, "/x/opencode.db", "T")
	require.NoError(t, err)
	assert.Equal(t, int64(15), tk.Total())
	assert.Equal(t, []string{"sqlite3", "-readonly", "/x/opencode.db"}, gotArgs[:3])

	// published: the draft becomes REPORT.md with the Cost: line under Head:, RESULT.md gains lines
	out := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(out, "REPORT.draft.md"), []byte("Verdict: LAND\nHead: abc\n\nbody\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(out, "RESULT.md"), []byte("RESULT: x\n"), 0o644))
	p := Price(row, "inception/mercury-2.5", Tokens{Input: 4_000_000})
	require.NoError(t, PublishCost(out, p, Tokens{Input: 4_000_000}, "inception/mercury-2.5"))
	rep, _ := os.ReadFile(filepath.Join(out, "REPORT.md"))
	l := strings.Split(string(rep), "\n")
	assert.Equal(t, "Verdict: LAND", l[0])
	assert.Equal(t, "Head: abc", l[1])
	assert.True(t, strings.HasPrefix(l[2], "Cost: $1.00 (opencode: $0.00) tokens input=4000000"), l[2])
	assert.NoFileExists(t, filepath.Join(out, "REPORT.draft.md"))
	res, _ := os.ReadFile(filepath.Join(out, "RESULT.md"))
	assert.Contains(t, string(res), "\ntokens: input=4000000")
	assert.Contains(t, string(res), "\ncost: $1.00 (opencode: $0.00)\n")
	// again: nothing doubles
	require.NoError(t, PublishCost(out, p, Tokens{Input: 4_000_000}, "m"))
	res2, _ := os.ReadFile(filepath.Join(out, "RESULT.md"))
	assert.Equal(t, string(res), string(res2))
	// unpriced rides on the report with its reason
	assert.Contains(t, WithCost("Verdict: HOLD\nHead: none\n", CostLine(Price(nil, "m", Tokens{}), Tokens{}, "m")), "Cost: unpriced (no route row for m")
	// no report: nothing written
	empty := t.TempDir()
	require.NoError(t, PublishCost(empty, p, Tokens{}, "m"))
	assert.NoFileExists(t, filepath.Join(empty, "REPORT.md"))
}

func TestLaneShimsRefuseGo(t *testing.T) {
	t.Parallel()
	dir, err := WriteShims(filepath.Join(t.TempDir(), "bin"), "vision")
	require.NoError(t, err)
	for _, tool := range []string{"go", "gofmt"} {
		fi, err := os.Stat(filepath.Join(dir, tool))
		require.NoError(t, err)
		assert.NotZero(t, fi.Mode()&0o111, tool+" is executable")
		body, _ := os.ReadFile(filepath.Join(dir, tool))
		assert.Contains(t, string(body), "exit 126")
		assert.Contains(t, string(body), "vision")
	}
	env := ShimEnv([]string{"PATH=/usr/bin", "GOROOT=/usr/local/go", "HOME=/h"}, dir)
	assert.Equal(t, []string{"PATH=" + dir + ":/usr/bin", "HOME=/h", "GOROOT=" + ShimGoroot}, env)
	assert.Equal(t, []string{"A=1", "PATH=" + dir, "GOROOT=" + ShimGoroot}, ShimEnv([]string{"A=1"}, dir))
}

func TestLaneFinishNote(t *testing.T) {
	t.Parallel()
	s, b := FinishNote("Freddy", "c~1", "Verdict: LAND", "$0.01", 42)
	assert.Equal(t, "Freddy card c~1: Verdict: LAND", s)
	assert.Equal(t, "Freddy one-shot lane finished c~1: Verdict: LAND; cost $0.01; wall 42s", b)
}

func TestLaneRulesAreReadOffTheRowAndTheMachine(t *testing.T) {
	t.Parallel()
	got := ParseLaneRules("FRIEND-BEAT OK row_mode=one-shot row_width=12 row_lane_rules={\"tiers\":[\"flash\"],\"load_max\":90,\"token_cap\":6000000}")
	assert.Equal(t, LaneRules{Tiers: []string{"flash"}, LoadMax: 90, TokenCap: 6000000}, got)
	assert.Equal(t, LaneRules{}, ParseLaneRules("FRIEND-BEAT OK row_mode=one-shot"))
	assert.Equal(t, LaneRules{}, ParseLaneRules("row_lane_rules={bad"))
	for in, want := range map[string]float64{"{ 1.5 2.0 3.0 }\n": 1.5, "0.25 0.5 1 1/200 99": 0.25} {
		l, ok := ParseLoad1(in)
		assert.True(t, ok)
		assert.Equal(t, want, l)
	}
	_, ok := ParseLoad1("")
	assert.False(t, ok)
}
