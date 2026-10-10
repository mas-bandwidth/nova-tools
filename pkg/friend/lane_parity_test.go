package friend

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What two friends' card runner scripts (runner.zsh) did that the one-shot lanes did not (the
// owner, 2026-10-05: "We need to get away from these one shot shell scripts"): each behaviour is
// a small function with a table test, and the ones that need the loop run through the lane rig.
func TestOpencodeLanesDoWhatTheRunnerStopgapsDid(t *testing.T) {
	t.Parallel()
	t.Run("the card filter", func(t *testing.T) {
		flash := LaneRules{Tiers: []string{"flash"}}
		security := LaneRules{Streams: []string{"security*", "fp-sec*", "sec-*"}}
		for _, c := range []struct {
			name             string
			r                LaneRules
			id, stream, tier string
			want             LaneVerdict
		}{
			{"no rules run everything", LaneRules{}, "c1", "general", "pro", LaneRun},
			{"flash works a flash card", flash, "c1", "general", "flash", LaneRun},
			{"flash takes a pro card back", flash, "c1", "general", "pro", LaneTake},
			{"flash takes a heavy card back", flash, "c1", "general", "heavy", LaneTake},
			{"an unknown tier is outside the tiers", flash, "c1", "general", "", LaneTake},
			{"a security stream runs", security, "c1", "security-audit", "pro", LaneRun},
			{"an fp-sec id runs", security, "fp-sec-12", "general", "pro", LaneRun},
			{"a sec- id runs", security, "sec-9", "general", "pro", LaneRun},
			{"any other card is skipped, not taken back", security, "c1", "general", "pro", LaneSkip},
			{"tier first: a pro security card is taken back from a flash-only friend", LaneRules{Tiers: []string{"flash"}, Streams: []string{"security*"}}, "c1", "security", "pro", LaneTake},
			{"both pass", LaneRules{Tiers: []string{"flash"}, Streams: []string{"security*"}}, "c1", "security", "flash", LaneRun},
			{"a bad pattern matches nothing", LaneRules{Streams: []string{"[x"}}, "c1", "x", "flash", LaneSkip},
		} {
			got, why := c.r.Judge(c.id, c.stream, c.tier)
			assert.Equal(t, c.want, got, "%s: %s", c.name, why)
			if c.want != LaneRun {
				assert.NotEmpty(t, why, c.name)
			}
		}
	})

	t.Run("the row configures the rules, never the code", func(t *testing.T) {
		for _, c := range []struct {
			name, answer string
			want         LaneRules
		}{
			{"nothing", "FRIEND-BEAT OK row_mode=one-shot row_width=8", LaneRules{}},
			{"all of it", "FRIEND-BEAT OK row_tiers=flash row_streams=security*,fp-sec* row_token_cap=6000000 row_load_max=90 row_load_width=3 row_pause_on=any row_refuse_go=1",
				LaneRules{Tiers: []string{"flash"}, Streams: []string{"security*", "fp-sec*"}, TokenCap: 6000000, LoadMax: 90, LoadWidth: 3, PauseOn: "any", RefuseGo: true}},
			{"a dash is none", "row_tiers=- row_token_cap=abc row_pause_on=never", LaneRules{}},
		} {
			assert.Equal(t, c.want, LaneRulesOf(c.answer), c.name)
		}
		flags := LaneRules{Tiers: []string{"flash"}, TokenCap: 1, LoadWidth: 3}
		assert.Equal(t, LaneRules{Tiers: []string{"pro"}, TokenCap: 1, LoadWidth: 3}, flags.Over(LaneRules{Tiers: []string{"pro"}}), "the row wins where it speaks")
	})

	t.Run("take back", func(t *testing.T) {
		assert.Equal(t, []string{"friend", "take", "bob", "c9", "--reason", "tier pro is outside"}, TakeArgv("bob", "c9", "tier pro is outside"))
		for _, c := range []struct {
			name, why, verb string
		}{
			{"plain", "tier pro is outside the tiers this friend works (flash)", "nova-sprint friend take bob c9 --reason 'tier pro is outside the tiers this friend works (flash)'"},
			{"a quote in the reason", "bob's tiers", `nova-sprint friend take bob c9 --reason 'bob'\''s tiers'`},
		} {
			subject, body := TakeBackNote("bob", "c9", "c9~15.g2", c.why)
			assert.Equal(t, "friend bob: take card c9 back for the dealer", subject, c.name)
			assert.Contains(t, body, "card c9 (job c9~15.g2): "+c.why, c.name)
			assert.Contains(t, body, "The sprint server serves no friend's take-back", c.name)
			assert.True(t, strings.HasSuffix(body, c.verb+"\n"), "%s: the coordinator's exact verb: %q", c.name, body)
		}
	})

	t.Run("the job name carries the generation", func(t *testing.T) {
		for _, c := range []struct {
			card  string
			epoch uint64
			gen   int
			want  string
		}{
			{"c1", 15, 1, "c1~15"},
			{"c1", 15, 0, "c1~15"},
			{"c1", 15, 2, "c1~15.g2"},
			{"c1", 15, 5, "c1~15.g5"},
			{"c1", 0, 1, "c1"},
		} {
			job := jobName(c.card, c.epoch, c.gen)
			assert.Equal(t, c.want, job)
			if c.epoch > 0 {
				id, epoch, gen, ok := ParseJob(job)
				assert.True(t, ok)
				assert.Equal(t, c.card, id)
				assert.EqualValues(t, c.epoch, epoch)
				assert.Equal(t, max(c.gen, 1), gen, "friend sync's own parse reads it back")
			}
		}
	})

	t.Run("width under load", func(t *testing.T) {
		r := LaneRules{LoadMax: 90}
		for _, c := range []struct {
			name  string
			r     LaneRules
			width int
			load  float64
			n     int
			held  bool
		}{
			{"no rule", LaneRules{}, 12, 500, 12, false},
			{"under the bound", r, 12, 89, 12, false},
			{"at the bound", r, 12, 90, 12, false},
			{"above it: held to the default", r, 12, 91, 3, true},
			{"above it: held to the row's", LaneRules{LoadMax: 90, LoadWidth: 2}, 12, 100, 2, true},
			{"a width at or under the lower number is never raised", r, 3, 500, 3, false},
			{"nor one lane", r, 1, 500, 1, false},
		} {
			n, held := c.r.LaneWidthUnderLoad(c.width, c.load)
			assert.Equal(t, c.n, n, c.name)
			assert.Equal(t, c.held, held, c.name)
		}
		for in, want := range map[string]float64{"{ 12.50 9.10 7.00 }": 12.5, "3.05 2.00 1.00 1/300 999": 3.05} {
			got, ok := Load1Of(in)
			assert.True(t, ok)
			assert.Equal(t, want, got)
		}
		_, ok := Load1Of("")
		assert.False(t, ok)
	})

	t.Run("the token cap", func(t *testing.T) {
		for _, c := range []struct {
			cap, tokens int64
			over        bool
		}{{0, 1 << 40, false}, {6000000, 5999999, false}, {6000000, 6000000, true}, {6000000, 9000000, true}} {
			assert.Equal(t, c.over, LaneRules{TokenCap: c.cap}.OverTokenCap(c.tokens), "%+v", c)
		}
		rep := TokenCapReport("bob", 6000000, 6100000, 2, "")
		lines := strings.Split(rep, "\n")
		assert.Equal(t, "Verdict: HOLD", lines[0])
		assert.Equal(t, "Head: none", lines[1])
		assert.Contains(t, rep, "token cap: 6100000 tokens, 2 turns, last step: unknown")
		assert.Contains(t, rep, "per-card cap of 6000000 tokens")
	})

	t.Run("a provider failure stops every lane", func(t *testing.T) {
		funds := OutOfFunds{Session: "s", Reason: "402 Payment Required: insufficient balance"}
		rate := RateLimited{Session: "s", Reason: "429 Too Many Requests"}
		for _, c := range []struct {
			name string
			r    LaneRules
			err  error
			msg  string
			stop bool
		}{
			{"out of funds always", LaneRules{}, funds, funds.Reason, true},
			{"a rate limit backs off by default", LaneRules{}, rate, "", false},
			{"a rate limit stops when the row says any", LaneRules{PauseOn: "any"}, rate, rate.Reason, true},
			{"funds only is the default spelled out", LaneRules{PauseOn: "funds"}, rate, "", false},
			{"wrapped is read", LaneRules{}, errors.Join(errors.New("turn"), funds), funds.Reason, true},
			{"another error is none", LaneRules{PauseOn: "any"}, errors.New("exit 1"), "", false},
		} {
			msg, stop := c.r.ProviderStop(c.err)
			assert.Equal(t, c.stop, stop, c.name)
			assert.Equal(t, c.msg, msg, c.name)
		}
		for _, c := range []struct {
			name, marker, model, reason string
		}{
			{"the marker's line, its time dropped", "2026-10-04T03:00:00Z 402 Payment Required: insufficient balance", "inception/mercury-2.5", "provider failure (inception/mercury-2.5): 402 Payment Required: insufficient balance"},
			{"a line with no time is the message", "429 Too Many Requests", "a/b", "provider failure (a/b): 429 Too Many Requests"},
			{"no model", "2026-10-04T03:00:00Z out of funds", "", "provider failure (-): out of funds"},
		} {
			until, reason := PauseBeat(c.marker, c.model, t0)
			assert.Equal(t, c.reason, reason, c.name)
			assert.Equal(t, t0.Add(PauseBeatAhead).UTC().Truncate(time.Second), until, c.name)
		}
		dir := t.TempDir()
		assert.Empty(t, ReadPause(dir))
		require.NoError(t, WritePause(dir, "402 Payment Required: insufficient balance", t0))
		assert.Contains(t, ReadPause(dir), "402 Payment Required: insufficient balance", "the exact message outlives the daemon")
		cleared, err := ClearPause(dir)
		require.NoError(t, err)
		assert.True(t, cleared)
		cleared, err = ClearPause(dir)
		require.NoError(t, err)
		assert.False(t, cleared)
		assert.Empty(t, ReadPause(dir))
	})

	t.Run("the cost line", func(t *testing.T) {
		route := RoutePrice{Name: "flash-mercury", Found: true, Prices: cardcost.Prices{Input: "0.25", CacheRead: "0.025", Output: "1", ReasoningAsOutput: true}}
		tok := func(in, cr, cw, out, rs int64) cardcost.Tokens {
			return cardcost.Tokens{Input: in, CacheRead: cr, CacheWrite: cw, Output: out, Reasoning: rs, Requests: -1, MaxPrompt: -1}
		}
		for _, c := range []struct {
			name string
			t    cardcost.Tokens
			rp   RoutePrice
			want string
		}{
			{"priced, rounded up to the cent", tok(1_000_000, 0, 0, 0, 0), route, "$0.25"},
			{"a fraction of a cent is a cent", tok(10, 0, 0, 0, 0), route, "$0.01"},
			{"reasoning is billed as output", tok(0, 0, 0, 500_000, 500_000), route, "$1.00"},
			{"cache read has its price", tok(0, 2_000_000, 0, 0, 0), route, "$0.05"},
			{"no route row", tok(1000, 0, 0, 0, 0), RoutePrice{}, "unpriced (no route row for inception/mercury-2.5 in the store's routes)"},
			{"a class with tokens and no price", tok(0, 0, 1000, 0, 0), route, "unpriced (route flash-mercury: no-price:cache_write)"},
			{"nothing reported", cardcost.None(), route, "unpriced (route flash-mercury: no-tokens)"},
		} {
			assert.Equal(t, c.want, CostOf(c.t, c.rp, "inception/mercury-2.5"), c.name)
		}

		report, tokens, cost := CostLine(LaneTokens{Tokens: tok(1_000_000, 0, 0, 0, 0), USD: "0.0123"}, route, "inception/mercury-2.5")
		assert.Equal(t, "cost: $0.25 (opencode: $0.02)", cost)
		assert.Equal(t, "tokens: input=1000000 cache_read=0 cache_write=0 output=0 reasoning=0 model=inception/mercury-2.5 harness=opencode", tokens)
		assert.Equal(t, "Cost: $0.25 (opencode: $0.02) tokens input=1000000 cache_read=0 cache_write=0 output=0 reasoning=0 model=inception/mercury-2.5 harness=opencode price_route=flash-mercury", report)

		// the line sits under Head:, once
		in := "Verdict: LAND\nHead: " + strings.Repeat("a", 40) + "\n\nthe paragraph\n"
		out := WithCost(in, report)
		assert.Equal(t, []string{"Verdict: LAND", "Head: " + strings.Repeat("a", 40), report, "", "the paragraph"}, strings.Split(strings.TrimRight(out, "\n"), "\n"))
		assert.Equal(t, out, WithCost(out, report), "a report that has one keeps it")
		assert.Equal(t, "Verdict: FAIL\n"+report+"\n", WithCost("Verdict: FAIL\n", report), "no Head: line: at the end")

		// published to the outbox: REPORT.md and RESULT.md
		box := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(box, "REPORT.md"), []byte(in), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(box, "RESULT.md"), []byte("RESULT: x\n"), 0o644))
		lt := LaneTokens{Tokens: tok(1_000_000, 0, 0, 0, 0)}
		require.NoError(t, PublishCost(box, lt, route, "inception/mercury-2.5"))
		require.NoError(t, PublishCost(box, lt, route, "inception/mercury-2.5"), "twice changes nothing")
		rep, _ := os.ReadFile(filepath.Join(box, "REPORT.md"))
		res, _ := os.ReadFile(filepath.Join(box, "RESULT.md"))
		assert.Equal(t, 1, strings.Count(string(rep), "Cost: $0.25"))
		assert.Equal(t, 1, strings.Count(string(res), "\ncost: $0.25"), string(res))
		assert.Contains(t, string(res), "tokens: input=1000000")
	})

	t.Run("tokens come from opencode's own database", func(t *testing.T) {
		sql, err := TokensSQL("ses_abc123")
		require.NoError(t, err)
		assert.Contains(t, sql, "id='ses_abc123' or parent_id='ses_abc123'")
		_, err = TokensSQL("x' or 1=1 --")
		assert.Error(t, err, "a session id is never spliced in unchecked")

		var gotName string
		var gotArgs []string
		run := func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
			gotName, gotArgs = name, args
			return "100|20|3|40|5|0.0125|2\n", 0, nil
		}
		got, err := TokensFromOpenCode(context.Background(), run, "/db/opencode.db", "ses_abc123")
		require.NoError(t, err)
		assert.Equal(t, "sqlite3", gotName)
		assert.Equal(t, []string{"-readonly", "/db/opencode.db"}, gotArgs[:2])
		assert.EqualValues(t, 100, got.Input)
		assert.EqualValues(t, 20, got.CacheRead)
		assert.EqualValues(t, 3, got.CacheWrite)
		assert.EqualValues(t, 40, got.Output)
		assert.EqualValues(t, 5, got.Reasoning)
		assert.Equal(t, 2, got.Sessions)
		assert.EqualValues(t, 168, got.Total())

		// a lane's session serves many cards: a card's tokens are the session's less its start
		base, _ := TokensOf("90|20|3|10|5|0.0100|2")
		d := got.Sub(base)
		assert.EqualValues(t, 10, d.Input)
		assert.EqualValues(t, 0, d.CacheRead)
		assert.EqualValues(t, 30, d.Output)
		assert.Equal(t, "0.0025", d.USD)

		for _, bad := range []string{"", "1|2|3", "a|2|3|4|5|6|7"} {
			_, err := TokensOf(bad)
			assert.Error(t, err, bad)
		}
		_, err = TokensFromOpenCode(context.Background(), func(context.Context, string, string, []string, string) (string, int, error) {
			return "", 1, nil
		}, "/db", "ses_1")
		assert.Error(t, err)
	})

	t.Run("the route row is the store's", func(t *testing.T) {
		routes := `{"routes":[{"name":"pro-x","provider":"p","model":"other","prices":{"input":"9"}},
			{"name":"flash-mercury","provider":"inception","model":"mercury-2.5","prices":{"input":"0.25","cache_read":"0.025","output":"1"}}]}`
		rp := RoutePriceOf(routes, "inception", "mercury-2.5")
		assert.True(t, rp.Found)
		assert.Equal(t, "flash-mercury", rp.Name)
		assert.Equal(t, "0.25", rp.Prices.Input)
		assert.True(t, rp.Prices.ReasoningAsOutput, "reasoning is billed as output unless the row says false")
		assert.False(t, RoutePriceOf(routes, "inception", "missing").Found)
		assert.False(t, RoutePriceOf("not json", "inception", "mercury-2.5").Found)
	})

	t.Run("go on the lane machine is refused by a shim on the lane's PATH", func(t *testing.T) {
		for argv0, want := range map[string]bool{"/x/shims/go": true, "gofmt": true, "nova-friend": false, "/usr/bin/gopls": false} {
			_, ok := GoShimName(argv0)
			assert.Equal(t, want, ok, argv0)
		}
		assert.Contains(t, GoRefusal("go"), "go is refused")
		assert.Contains(t, GoRefusal("go"), "; run: ", "a refusal says the next step")

		dir := filepath.Join(t.TempDir(), ShimDirName)
		self := filepath.Join(t.TempDir(), "nova-friend")
		require.NoError(t, os.WriteFile(self, []byte("x"), 0o755))
		require.NoError(t, GoShims(dir, self))
		require.NoError(t, GoShims(dir, self), "again is the same")
		for _, name := range GoShimNames {
			to, err := os.Readlink(filepath.Join(dir, name))
			require.NoError(t, err)
			assert.Equal(t, self, to, "%s is this binary: no script", name)
		}

		var gotName string
		var gotArgs []string
		inner := func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
			gotName, gotArgs = name, args
			return "", 0, nil
		}
		shim := ShimExec(inner, dir, "/usr/bin:/bin")
		_, _, _ = shim(LaneContext(context.Background()), "/w", "opencode", []string{"run", "x"}, "")
		assert.Equal(t, "env", gotName)
		assert.Equal(t, []string{"PATH=" + dir + ":/usr/bin:/bin", "GOROOT=" + NoGoRoot, "opencode", "run", "x"}, gotArgs)
		_, _, _ = shim(context.Background(), "/w", "opencode", []string{"run"}, "")
		assert.Equal(t, "opencode", gotName, "outside a lane nothing is wrapped")
	})

	t.Run("a bus note at each finish", func(t *testing.T) {
		subject, body := FinishNote("bob", "c1~15", "LAND", "$0.25 (opencode: $0.02)", 95*time.Second)
		assert.Equal(t, "bob card c1~15: LAND", subject)
		assert.Equal(t, "bob one-shot lane finished c1~15: LAND; cost $0.25 (opencode: $0.02); wall 1m35s\n", body)
		subject, _ = FinishNote("bob", "c1~15", "", "unpriced", time.Second)
		assert.Equal(t, "bob card c1~15: no report", subject)
	})
}

// serverSprint is the sprint server as a friend's daemon meets it: a worker's verb answered,
// a coordinator's (friend take, friend down, ...) refused with the server's words; each
// coordinator verb asked is kept in asked.
func serverSprint(mu *sync.Mutex, asked *[][]string) func(context.Context, []string) (string, error) {
	return func(_ context.Context, argv []string) (string, error) {
		if len(argv) >= 2 && argv[0] == "friend" && argv[1] != "beat" && argv[1] != "cards" {
			mu.Lock()
			*asked = append(*asked, argv)
			mu.Unlock()
			return "", errors.New("the server runs the workers' verbs only: take, finish, progress, read, queue, fleet beat, friend beat, friend cards, lane take, lane give")
		}
		return "", nil
	}
}

// the rules and the rest of the parity, wired into the lanes' loop.
func parityRig(t *testing.T, h *lanesHarness, width int, rules LaneRules) *rig {
	t.Helper()
	r, _ := laneRig(t, h, width)
	r.d.Rules = func() LaneRules { return rules }
	r.d.Model = "inception/mercury-2.5"
	r.d.Route = func() RoutePrice {
		return RoutePrice{Name: "flash-mercury", Found: true, Prices: cardcost.Prices{Input: "1", Output: "1", ReasoningAsOutput: true}}
	}
	return r
}

func TestLanesTakeBackACardOutsideTheRowsTiersAndRunOnlyTheRest(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c9", "queued"}, {"c5", "queued"}}, []string{"c1", "c9", "c5"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true, "c9": true, "c5": true}, active: map[string]int{}}
		r := parityRig(t, h, 2, LaneRules{Tiers: []string{"flash"}})
		r.d.heldCards = []HeldCard{
			{Card: "c1", Job: "c1~15", Col: "working", Tier: "flash"},
			{Card: "c9", Job: "c9~15", Col: "working", Tier: "pro"},
			{Card: "c5", Job: "c5~15", Col: "working", Tier: "pro"},
		}
		// c5 was started outside the lanes: a jobs directory exists, so it is not taken back
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", "c5~15"), 0o755))
		var mu sync.Mutex
		var asked [][]string
		r.d.Sprint = serverSprint(&mu, &asked)
		r.run(t, 20)

		turns, _, _ := h.got()
		require.Len(t, turns, 1, "only the flash card ran: %v", turns)
		assert.True(t, strings.HasSuffix(turns[0], ": c1"), "%v", turns)
		mu.Lock()
		defer mu.Unlock()
		assert.Empty(t, asked, "no coordinator verb is sent: the server refuses a friend's take")
		var notes []bus.Message
		for _, m := range r.adaMessages(t) {
			if m.Kind == bus.KindRequest {
				notes = append(notes, m)
			}
		}
		require.Len(t, notes, 1, "the pro card, once; c5 was started outside the lanes: %v", notes)
		assert.Equal(t, "friend bob: take card c9 back for the dealer", notes[0].Subject)
		assert.Contains(t, notes[0].Body, "nova-sprint friend take bob c9 --reason 'tier pro is outside the tiers this friend works (flash); taken back for the dealer'")
		assert.Contains(t, strings.Join(r.records, "\n"), "take c9 back: asked the coordinator")
	})
}

func TestLanesAreHeldToTheLoadWidthWhileTheLoadIsHigh(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}, {"c3", "queued"}, {"c4", "queued"}}, []string{"c1", "c2", "c3", "c4"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true, "c2": true, "c3": true, "c4": true}, active: map[string]int{}, block: make(chan struct{})}
		r := parityRig(t, h, 4, LaneRules{LoadMax: 90, LoadWidth: 1})
		var load atomic.Int64
		load.Store(95)
		r.d.Load = func() float64 { return float64(load.Load()) }
		var high, low int
		r.at[15] = func() { turns, _, _ := h.got(); high = len(turns); load.Store(10) }
		r.at[40] = func() { turns, _, _ := h.got(); low = len(turns); close(h.block) }
		r.run(t, 50)
		assert.Equal(t, 1, high, "above the bound one lane runs, of four")
		assert.Equal(t, 4, low, "below it every lane does")
		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "load above 90: lanes held at 1 of 4")
		assert.Contains(t, records, "load at or below 90: lanes back to 4")
	})
}

// A card over the row's token cap gets a HOLD report naming the cap and its lane is stopped;
// its finish publishes the cost on REPORT.md and RESULT.md and sends the bus note.
func TestACardOverTheTokenCapIsHeldAndItsCostPublished(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, active: map[string]int{}, block: make(chan struct{})} // the turn runs until it is stopped
		r := parityRig(t, h, 1, LaneRules{TokenCap: 6_000_000})
		var reads atomic.Int64
		r.d.Tokens = func(context.Context, string) (LaneTokens, error) {
			// the first read is the card's start; every later one says it has spent 7M input tokens
			if reads.Add(1) == 1 {
				return LaneTokens{Tokens: cardcost.Tokens{Requests: -1, MaxPrompt: -1}, USD: "1"}, nil
			}
			return LaneTokens{Tokens: cardcost.Tokens{Input: 7_000_000, Requests: -1, MaxPrompt: -1}, USD: "8.5"}, nil
		}
		r.run(t, 60)

		rep, err := os.ReadFile(filepath.Join(dir, "outbox", "c1~15", "REPORT.md"))
		require.NoError(t, err)
		lines := strings.Split(string(rep), "\n")
		assert.Equal(t, "Verdict: HOLD", lines[0])
		assert.Equal(t, "Head: none", lines[1])
		assert.Equal(t, "Cost: $7.00 (opencode: $7.50) tokens input=7000000 cache_read=0 cache_write=0 output=0 reasoning=0 model=inception/mercury-2.5 harness=opencode price_route=flash-mercury", lines[2])
		assert.Contains(t, string(rep), "per-card cap of 6000000 tokens")
		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "TOKEN CAP lane=1 card=c1: 7000000 tokens of 6000000; lane stopped")
		assert.Contains(t, records, "card=done")
		got := r.adaGot(t)
		require.Len(t, got, 1, "one bus note, at the finish: %v", got)
		assert.Contains(t, got[0], "bob card c1~15: HOLD")
		assert.Contains(t, got[0], "cost $7.00")
	})
}

// A card that finishes says its cost on REPORT.md and RESULT.md, unpriced with its reason when
// the store has no route row, and tells the coordinator.
func TestAFinishedCardPublishesItsCostOrWhyNot(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		route RoutePrice
		cost  string
	}{
		{"priced", RoutePrice{Name: "flash-mercury", Found: true, Prices: cardcost.Prices{Input: "1", ReasoningAsOutput: true}}, "cost: $2.00 (opencode: $2.10)"},
		{"unpriced", RoutePrice{}, "cost: unpriced (no route row for inception/mercury-2.5 in the store's routes) (opencode: $2.10)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
				h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{}}
				r := parityRig(t, h, 1, LaneRules{})
				r.d.Route = func() RoutePrice { return c.route }
				var reads atomic.Int64
				r.d.Tokens = func(context.Context, string) (LaneTokens, error) {
					if reads.Add(1) == 1 {
						return LaneTokens{Tokens: cardcost.Tokens{Requests: -1, MaxPrompt: -1}}, nil
					}
					return LaneTokens{Tokens: cardcost.Tokens{Input: 2_000_000, Requests: -1, MaxPrompt: -1}, USD: "2.1"}, nil
				}
				r.run(t, 20)
				res, err := os.ReadFile(filepath.Join(dir, "outbox", "c1~15", "RESULT.md"))
				require.NoError(t, err)
				assert.Contains(t, string(res), c.cost)
				rep, err := os.ReadFile(filepath.Join(dir, "outbox", "c1~15", "REPORT.md"))
				require.NoError(t, err)
				assert.Contains(t, string(rep), "\nCost: "+strings.TrimPrefix(c.cost, "cost: "))
				msgs := r.adaMessages(t)
				require.Len(t, msgs, 1)
				assert.Contains(t, msgs[0].Subject, "bob card c1~15")
			})
		})
	}
}

// A provider failure stops every lane, holds the friend down with the exact message, and
// nothing resumes until a person clears the pause (nova-friend resume); out of funds always,
// a rate limit when the row says pause_on=any.
func TestAProviderFailureHoldsTheFriendDownUntilAPersonClearsIt(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		rules LaneRules
		err   error
		stops bool
	}{
		{"out of funds", LaneRules{}, OutOfFunds{Session: "s", Reason: "402 Payment Required: insufficient balance"}, true},
		{"a rate limit with pause_on=any", LaneRules{PauseOn: "any"}, RateLimited{Session: "s", Reason: "429 Too Many Requests"}, true},
		{"a rate limit by default backs off, no hold", LaneRules{}, RateLimited{Session: "s", Reason: "429 Too Many Requests"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r, h, _ := limitRig(t, 2, 6)
				r.d.Rules = func() LaneRules { return c.rules }
				h.setAnswer(func() error { return c.err })
				var mu sync.Mutex
				var downs []string
				marker := ""
				r.d.LaneHoldDown = func(_ context.Context, message string) error {
					mu.Lock()
					defer mu.Unlock()
					downs, marker = append(downs, message), message
					return nil
				}
				r.d.LaneHold = func() string { mu.Lock(); defer mu.Unlock(); return marker }
				var heldAt, atClear int
				r.at[30] = func() { heldAt = len(h.turnStarts()) }
				r.at[100] = func() { // a person cleared the pause
					mu.Lock()
					marker = ""
					mu.Unlock()
					h.setAnswer(nil)
					atClear = len(h.turnStarts())
				}
				r.run(t, 200)

				records := strings.Join(r.records, "\n")
				mu.Lock()
				defer mu.Unlock()
				if !c.stops {
					assert.Empty(t, downs)
					assert.NotContains(t, records, "provider failure")
					return
				}
				var reason string
				switch e := c.err.(type) {
				case OutOfFunds:
					reason = e.Reason
				case RateLimited:
					reason = e.Reason
				}
				assert.Equal(t, []string{reason}, downs, "held down once, with the provider's exact message")
				assert.LessOrEqual(t, heldAt, 2, "at most the turns already under way; none after the hold")
				assert.Greater(t, len(h.turnStarts()), atClear, "after a person clears the pause the lanes run again: %s", records)
				assert.Contains(t, records, "the pause is cleared by a person; lanes resume")
				var blockers int
				for _, m := range r.adaMessages(t) {
					if m.Kind == bus.KindBlocker {
						blockers++
					}
				}
				assert.Equal(t, 1, blockers, "one judgment to the coordinator")
			})
		})
	}
}

// stopHarness is a lanes harness where c1's first turn runs until the daemon ends it and
// c2's first turn, once c1's is under way, meets the provider's failure; every later turn
// runs as the lanes harness's do.
type stopHarness struct {
	*lanesHarness
	failure   error
	mu        sync.Mutex
	calls     map[string]int
	c1Running chan struct{}
	c1Ended   bool // c1's first turn ended by the daemon, not by itself
}

func (h *stopHarness) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
	id := cardOfText.FindStringSubmatch(text)[1]
	h.mu.Lock()
	h.calls[id]++
	n := h.calls[id]
	h.mu.Unlock()
	switch {
	case id == "c1" && n == 1:
		close(h.c1Running)
		<-ctx.Done()
		h.mu.Lock()
		h.c1Ended = true
		h.mu.Unlock()
		return LaneTurn{Exit: -1}, ctx.Err()
	case id == "c2" && n == 1:
		select {
		case <-h.c1Running:
		case <-ctx.Done():
		}
		return LaneTurn{Exit: 1}, h.failure
	}
	return h.lanesHarness.DeliverTo(ctx, session, text)
}

// A provider failure in one lane stops the other lanes under way at once, as the runner's
// pause killed every lane: each stopped card is kept in its lane's hand, counted toward
// nothing, never set aside, and runs again once a person clears the pause; the friend is
// held down once with the provider's exact message.
func TestAProviderFailureStopsEveryLaneUnderWayAndKeepsItsCard(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		rules   LaneRules
		failure error
		reason  string
	}{
		{"out of funds", LaneRules{}, OutOfFunds{Session: "s", Reason: "402 Payment Required: insufficient balance"}, "402 Payment Required: insufficient balance"},
		{"a rate limit with pause_on=any", LaneRules{PauseOn: "any"}, RateLimited{Session: "s", Reason: "429 Too Many Requests: rate limit"}, "429 Too Many Requests: rate limit"},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
				lh := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true, "c2": true}, active: map[string]int{}}
				h := &stopHarness{lanesHarness: lh, failure: c.failure, calls: map[string]int{}, c1Running: make(chan struct{})}
				r := parityRig(t, lh, 2, c.rules)
				r.d.Deliver = h
				var mu sync.Mutex
				var downs []string
				marker := ""
				r.d.LaneHoldDown = func(_ context.Context, message string) error {
					mu.Lock()
					defer mu.Unlock()
					downs, marker = append(downs, message), message
					return nil
				}
				r.d.LaneHold = func() string { mu.Lock(); defer mu.Unlock(); return marker }
				var heldCalls map[string]int
				r.at[80] = func() { // a person clears the pause
					h.mu.Lock()
					heldCalls = maps.Clone(h.calls)
					h.mu.Unlock()
					mu.Lock()
					marker = ""
					mu.Unlock()
				}
				r.run(t, 160)

				records := strings.Join(r.records, "\n")
				h.mu.Lock()
				defer h.mu.Unlock()
				assert.True(t, h.c1Ended, "c1's turn under way was ended by the daemon, not left running: %s", records)
				assert.Equal(t, map[string]int{"c1": 1, "c2": 1}, heldCalls, "no turn while held")
				assert.Regexp(t, `lane \d: card c1 stopped: a provider failure stops every lane; the card is kept and runs again after a person resumes`, records)
				assert.Regexp(t, `subject="card c1"[^\n]*card=kept turn=0/\d+ reason="a provider failure stopped every lane"`, records, "kept, counted toward nothing")
				assert.NotContains(t, records, "card=set_aside")
				assert.NotRegexp(t, `subject="card c1"[^\n]*card=again`, records)
				assert.Contains(t, records, "nothing resumes until a person clears "+PauseFile)
				mu.Lock()
				assert.Equal(t, []string{c.reason}, downs, "held down once, with the provider's exact message")
				mu.Unlock()
				assert.Contains(t, records, "the pause is cleared by a person; lanes resume")
				for _, id := range []string{"c1", "c2"} {
					assert.Equal(t, 2, h.calls[id], "%s runs again after the resume", id)
					assert.FileExists(t, filepath.Join(dir, "outbox", id+"~15", "RESULT.md"))
				}
			})
		})
	}
}

// A pause marker that cannot be written leaves the hold in the daemon: it is never read as a
// person's clearing, so the lanes stay held (the finding of opencode-lanes-parity-r2.w2).
func TestAPauseMarkerNotWrittenIsNotAResume(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r, h, _ := limitRig(t, 2, 4)
		r.d.Rules = func() LaneRules { return LaneRules{} }
		h.setAnswer(func() error { return OutOfFunds{Session: "s", Reason: "402 Payment Required"} })
		r.d.LaneHoldDown = func(context.Context, string) error { return errors.New("read-only file system") }
		r.d.LaneHold = func() string { return "" }
		var heldAt int
		r.at[20] = func() { heldAt = len(h.turnStarts()); h.setAnswer(nil) }
		r.run(t, 120)
		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "the pause marker cannot be written, so the friend is not beaten down and the lanes stay held until this daemon restarts: read-only file system")
		assert.NotContains(t, records, "cleared by a person")
		assert.Equal(t, heldAt, len(h.turnStarts()), "no turn after the hold: %s", records)
	})
}

// jobName is the job directory friend sync names for a card dealt at epoch: <card>~<epoch>,
// with .g<gen> after it from the second generation (friendJobOf); a card with no epoch is
// its id alone.
func jobName(card string, epoch uint64, gen int) string {
	job := card
	if epoch > 0 {
		job += "~" + strconv.FormatUint(epoch, 10)
	}
	if gen > 1 {
		job += ".g" + strconv.Itoa(gen)
	}
	return job
}
