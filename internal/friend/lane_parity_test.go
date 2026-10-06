package friend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The opencode one-shot lanes do what two friends' runner.zsh stopgaps did
// (2026-10-04/05), each behaviour its own table, configured on the friend's row.
func TestOpencodeLanesDoWhatTheRunnerStopgapsDid(t *testing.T) {
	t.Parallel()

	t.Run("the row's lane settings are read off the beat's answer", func(t *testing.T) {
		t.Parallel()
		r := ParseLaneRow("row_mode=one-shot row_width=12 row_tiers=Flash row_streams=security*,sec-* row_token_cap=6000000 row_load_max=90 row_load_width=3 row_provider_stop=true row_model=inception/mercury-2.5 row_route=flash-mercury row_price_input=0.25 row_price_output=0.75")
		assert.Equal(t, []string{"flash"}, r.Tiers)
		assert.Equal(t, []string{"security*", "sec-*"}, r.Streams)
		assert.Equal(t, int64(6000000), r.TokenCap)
		assert.Equal(t, 90.0, r.LoadMax)
		assert.True(t, r.StopOnProvider)
		assert.Equal(t, "flash-mercury", r.Route.Name)
		assert.True(t, r.Set)
		assert.Equal(t, LaneRow{Route: RouteRow{ReasoningAsOutput: true}}, ParseLaneRow("row_mode=batch row_width=2 row_profile=p"))
	})

	t.Run("filter", func(t *testing.T) {
		t.Parallel()
		flash := LaneRow{Tiers: []string{"flash"}}
		sec := LaneRow{Streams: []string{"security*", "sec-*", "fp-sec*"}}
		for _, tc := range []struct {
			name             string
			row              LaneRow
			id, stream, tier string
			want             FilterAction
		}{
			{"no filter runs everything", LaneRow{}, "a", "x", "pro", FilterRun},
			{"her tier runs", flash, "a", "x", "flash", FilterRun},
			{"tier is case-blind", flash, "a", "x", "Flash", FilterRun},
			{"another tier is taken back", flash, "a", "x", "pro", FilterTake},
			{"no tier is taken back", flash, "a", "x", "", FilterTake},
			{"stream glob runs", sec, "a", "security-rocketnet", "pro", FilterRun},
			{"id glob runs", sec, "sec-rocketnet-keys", "net", "pro", FilterRun},
			{"fp id runs", sec, "fp-sec-1", "", "", FilterRun},
			{"other stream is skipped, not taken", sec, "wake-ping", "fleet", "pro", FilterSkip},
			{"tier decides before stream", LaneRow{Tiers: []string{"flash"}, Streams: []string{"sec*"}}, "sec-a", "security", "pro", FilterTake},
		} {
			got, why := tc.row.Filter(tc.id, tc.stream, tc.tier)
			assert.Equal(t, tc.want, got, tc.name)
			if got != FilterRun {
				assert.NotEmpty(t, why, tc.name)
			}
		}
	})

	t.Run("take back", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{"friend", "take", "bob", "wake-ping-r2", "--reason", "tier pro: only flash"}, TakeArgv("bob", "wake-ping-r2", "tier pro: only flash"))
		assert.Equal(t, 300, len(TakeArgv("f", "c", strings.Repeat("x", 900))[5]))
	})

	t.Run("generation job name", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			card, epoch string
			gen         int
			want        string
		}{
			{"c", "15", 1, "c~15"}, {"c", "15", 0, "c~15"}, {"c", "15", 2, "c~15.g2"}, {"c", "", 1, "c"}, {"c", "", 3, "c.g3"},
		} {
			got := JobName(tc.card, tc.epoch, tc.gen)
			assert.Equal(t, tc.want, got)
			id, _, gen, ok := ParseJob(got)
			if tc.epoch != "" {
				require.True(t, ok)
				assert.Equal(t, tc.card, id)
				assert.Equal(t, max(tc.gen, 1), gen, "friend sync's own parser reads the name back")
			}
		}
	})

	t.Run("width under load", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name  string
			row   LaneRow
			width int
			load  float64
			want  int
			held  bool
		}{
			{"no bound", LaneRow{}, 12, 500, 12, false},
			{"under the bound", LaneRow{LoadMax: 90}, 12, 90, 12, false},
			{"above the bound is held to 3", LaneRow{LoadMax: 90}, 12, 91, 3, true},
			{"above the bound is held to the row's number", LaneRow{LoadMax: 90, LoadWidth: 5}, 12, 120, 5, true},
			{"never raised", LaneRow{LoadMax: 90, LoadWidth: 5}, 2, 120, 2, false},
		} {
			n, held := tc.row.LaneWidth(tc.width, tc.load)
			assert.Equal(t, tc.want, n, tc.name)
			assert.Equal(t, tc.held, held, tc.name)
		}
	})

	t.Run("token cap HOLD", func(t *testing.T) {
		t.Parallel()
		row := LaneRow{TokenCap: 1000}
		for _, tc := range []struct {
			u    TokenUsage
			over bool
		}{{TokenUsage{Input: 999}, false}, {TokenUsage{Input: 900, Output: 100}, true}, {TokenUsage{CacheRead: 600, Reasoning: 500}, true}} {
			assert.Equal(t, tc.over, row.OverCap(tc.u))
		}
		assert.False(t, LaneRow{}.OverCap(TokenUsage{Input: 1 << 40}), "no cap, no stop")
		c := Card{ID: "k", Outbox: filepath.Join(t.TempDir(), "outbox", "k~15")}
		require.NoError(t, WriteCapReport(c, "bob", 1000, TokenUsage{Input: 1200}, "← bash"))
		raw, err := os.ReadFile(c.Report())
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(string(raw), "Verdict: HOLD\nHead: none\n"))
		assert.Contains(t, string(raw), "per-card cap of 1000 tokens")
		assert.Contains(t, string(raw), "1200 tokens")
	})

	t.Run("provider failure", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name, out, want string
		}{
			{"402", "x\nError: request failed with status 402\n", "Error: request failed with status 402"},
			{"429", "\x1b[31mError: 429 Too Many Requests\x1b[0m", "Error: 429 Too Many Requests"},
			{"out of funds", "Error: insufficient credits on this key", "Error: insufficient credits on this key"},
			{"rate limit", "Error: rate limit reached for mercury", "Error: rate limit reached for mercury"},
			{"not an Error line", "the card mentions a 429 rate limit", ""},
			{"a port is no status", "Error: connect 127.0.0.1:4290 refused", ""},
			{"clean", "done", ""},
		} {
			assert.Equal(t, tc.want, ProviderFailureLine(tc.out), tc.name)
		}
		assert.Equal(t, []string{"friend", "down", "carol", "--reason", "provider failure (abl/m): Error: 402"}, DownArgv("carol", "abl/m", "Error: 402"))
		err := RateLimited{Session: "s", Reason: "Error: 429"}
		assert.Equal(t, "Error: 429", err.Reason)
	})

	t.Run("cost line", func(t *testing.T) {
		t.Parallel()
		route := RouteRow{Name: "flash-mercury", Input: "0.25", CacheRead: "0.025", Output: "0.75", ReasoningAsOutput: true}
		for _, tc := range []struct {
			name  string
			route RouteRow
			u     TokenUsage
			want  string
		}{
			{"rounded up to the cent", route, TokenUsage{Input: 1_000_001}, "$0.26"},
			{"exact stays", route, TokenUsage{Input: 4_000_000}, "$1.00"},
			{"reasoning priced as output", route, TokenUsage{Output: 1_000_000, Reasoning: 1_000_000}, "$1.50"},
			{"reasoning kept apart when the row says", RouteRow{Name: "r", Output: "1", ReasoningAsOutput: false}, TokenUsage{Output: 1_000_000, Reasoning: 5_000_000}, "$1.00"},
			{"nothing used", route, TokenUsage{}, "$0.00"},
			{"no row", RouteRow{}, TokenUsage{Input: 5}, "unpriced (no route row for inception/mercury-2.5 in nova-sprint routes)"},
			{"missing price", RouteRow{Name: "r", Input: "1"}, TokenUsage{CacheWrite: 5}, "unpriced (route r has no cache_write price)"},
			{"long context", RouteRow{Name: "r", Input: "1", Extra: true}, TokenUsage{Input: 5}, "unpriced (route r has long-context, request or gateway prices this pricer does not apply)"},
		} {
			assert.Equal(t, tc.want, tc.route.Cost("inception/mercury-2.5", tc.u), tc.name)
		}
		u, err := ParseUsage("100|20|3|40|5|0.0123|2\n")
		require.NoError(t, err)
		assert.Equal(t, TokenUsage{100, 20, 3, 40, 5, 0.0123, 2}, u)
		assert.Equal(t, int64(168), u.Total())
		assert.Equal(t, TokenUsage{Input: 90}, TokenUsage{Input: 100}.Sub(TokenUsage{Input: 10}))
		for _, bad := range []string{"", "1|2", "a|0|0|0|0|0|1"} {
			_, err := ParseUsage(bad)
			assert.Error(t, err, bad)
		}
		assert.Contains(t, UsageSQL("ses_1'x"), "id='ses_1''x' or parent_id='ses_1''x'")

		// read through the Exec seam: sqlite3 read-only on the opencode database
		var got []string
		run := func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
			got = append([]string{name}, args...)
			return "1|2|3|4|5|0|1\n", 0, nil
		}
		u, err = ReadUsage(context.Background(), run, "/h/opencode.db", "ses_1")
		require.NoError(t, err)
		assert.Equal(t, int64(15), u.Total())
		assert.Equal(t, []string{"sqlite3", "-readonly", "/h/opencode.db"}, got[:3])

		// published: the Cost: line under Head: on REPORT.md, tokens/cost on RESULT.md, once
		c := Card{ID: "k", Outbox: t.TempDir()}
		require.NoError(t, os.WriteFile(c.Report(), []byte("Verdict: LAND\nHead: "+strings.Repeat("a", 40)+"\n\nGates green.\n"), 0o644))
		require.NoError(t, os.WriteFile(c.Result(), []byte("RESULT: k sha=x"), 0o644))
		for range 2 {
			require.NoError(t, PublishCost(c, "inception/mercury-2.5", route, TokenUsage{Input: 1_000_001}))
		}
		rep, _ := os.ReadFile(c.Report())
		lines := strings.Split(string(rep), "\n")
		assert.Equal(t, "Verdict: LAND", lines[0])
		assert.True(t, strings.HasPrefix(lines[1], "Head: "))
		assert.True(t, strings.HasPrefix(lines[2], "Cost: $0.26 (opencode: $0.00) tokens input=1000001"), lines[2])
		assert.Equal(t, 1, strings.Count(string(rep), "Cost: "), "published once")
		res, _ := os.ReadFile(c.Result())
		assert.Equal(t, 1, strings.Count(string(res), "cost: $0.26"))
		assert.Contains(t, string(res), "tokens: input=1000001")
		assert.Equal(t, "Verdict: HOLD\nCost: x\n", WithCost("Verdict: HOLD\n", "Cost: x"), "no Head: line: at the end")
	})

	t.Run("go refusal shims", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "shims")
		made, err := WriteShims(dir, "/bin/nova-friend")
		require.NoError(t, err)
		assert.Equal(t, []string{"go", "gofmt"}, made)
		for _, n := range ShimNames {
			to, err := os.Readlink(filepath.Join(dir, n))
			require.NoError(t, err)
			assert.Equal(t, "/bin/nova-friend", to)
		}
		made, err = WriteShims(dir, "/bin/nova-friend")
		require.NoError(t, err)
		assert.Empty(t, made, "a second call changes nothing")
		for _, tc := range []struct {
			argv0 string
			is    bool
		}{{"go", true}, {"/x/shims/gofmt", true}, {"/usr/bin/nova-friend", false}, {"gopls", false}} {
			refusal, code, is := ShimMain(tc.argv0)
			assert.Equal(t, tc.is, is, tc.argv0)
			if is {
				assert.Equal(t, ShimExit, code)
				assert.Contains(t, refusal, "bench")
			}
		}
		env := LaneEnv([]string{"A=1", "PATH=/usr/bin", "GOROOT=/usr/local/go"}, "/s")
		assert.Equal(t, []string{"A=1", "PATH=/s:/usr/bin", "GOROOT=" + NoGoRoot}, env)
		assert.Equal(t, []string{"PATH=/s", "GOROOT=" + NoGoRoot}, LaneEnv(nil, "/s"))
	})

	t.Run("finish note", func(t *testing.T) {
		t.Parallel()
		c := Card{ID: "k", Outbox: "/w/outbox/k~15.g2"}
		subject, body := FinishNote("bob", c, "Verdict: LAND", "$0.26", 95*time.Second+400*time.Millisecond)
		assert.Equal(t, "bob card k~15.g2: Verdict: LAND", subject)
		assert.Contains(t, body, "cost $0.26; wall 1m35s")
		subject, _ = FinishNote("bob", c, "", "unmeasured", time.Second)
		assert.Equal(t, "bob card k~15.g2: no report", subject)
	})
}
