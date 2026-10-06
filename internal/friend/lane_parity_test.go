package friend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parityHarness is an opencode-like lane harness: sessions ses_<lane>, and each card's turn
// run by the test's turn function (the default writes nothing and ends at once). It keeps the
// turns it was handed and the shim directory each turn's context carried.
type parityHarness struct {
	mu    sync.Mutex
	dir   string
	turns []string
	shims []string
	turn  func(ctx context.Context, id string) (LaneTurn, error)
}

func (h *parityHarness) Deliver(context.Context, string) (int, error) { return 0, nil }

func (h *parityHarness) OpenSession(_ context.Context, seed string) (string, error) {
	return "ses_" + laneOfSeed.FindStringSubmatch(seed)[1], nil
}

func (h *parityHarness) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
	id := cardOfText.FindStringSubmatch(text)[1]
	h.mu.Lock()
	h.turns = append(h.turns, session+": "+id)
	h.shims = append(h.shims, shimsOf(ctx))
	turn := h.turn
	h.mu.Unlock()
	if turn == nil {
		return LaneTurn{}, nil
	}
	return turn(ctx, id)
}

func (h *parityHarness) got() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.turns...)
}

// writeOut writes the card's outbox file name (REPORT.md, RESULT.md) in dir.
func writeOut(dir, id, name, text string) error {
	out := filepath.Join(dir, "outbox", id+"~15")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, name), []byte(text), 0o644)
}

// finishCard is a turn that writes the card's REPORT.md and RESULT.md, a LAND.
func finishCard(dir string) func(context.Context, string) (LaneTurn, error) {
	return func(_ context.Context, id string) (LaneTurn, error) {
		if err := writeOut(dir, id, "REPORT.md", "Verdict: LAND\nHead: 0123456789012345678901234567890123456789\n\nthe gate is green\n"); err != nil {
			return LaneTurn{}, err
		}
		return LaneTurn{}, writeOut(dir, id, "RESULT.md", "RESULT: "+id+"\n")
	}
}

// parityRig is the lanes rig over h at width lanes with the runner's behaviours p.
func parityRig(t *testing.T, h *parityHarness, width int, p *LaneParity) (*rig, *LaneState) {
	r := newRig(t)
	state := &LaneState{}
	r.d.Deliver, r.passive, r.d.Dir = h, true, h.dir
	r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
	r.d.Row = func() (string, int) { return ModeOneShot, width }
	r.d.Coordinator = "ada"
	r.d.LoadLanes = func() (LaneState, error) { return *state, nil }
	r.d.SaveLanes = func(s LaneState) error { *state = s; return nil }
	r.d.Parity = p
	return r, state
}

// usageFake is opencode's database as the lanes read it: each session's tokens, set by the test.
type usageFake struct {
	mu sync.Mutex
	by map[string]TokenUsage
}

func (u *usageFake) set(session string, v TokenUsage) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.by[session] = v
}

func (u *usageFake) read(_ context.Context, session string) (TokenUsage, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.by[session], nil
}

// Each of the runner.zsh stopgaps' behaviours (two friends' copies of one runner, 2026-10-04/05) is a
// small function the opencode one-shot lane's turn calls: a table per behaviour, then the lanes
// run over a fake harness showing the turn calling it. No friend take or friend down is sent:
// both are coordinator verbs the sprint server refuses from a friend, so each is a blocker to
// the coordinator with the line to run.
func TestOpencodeLanesDoWhatTheRunnerStopgapsDid(t *testing.T) {
	t.Parallel()

	t.Run("filter", func(t *testing.T) {
		flash := LaneParity{Tiers: []string{"flash"}}
		security := LaneParity{Streams: []string{"security*", "sec-*", "fp-sec*"}}
		for _, tc := range []struct {
			name             string
			p                LaneParity
			id, stream, tier string
			want             FilterAction
			why              string
		}{
			{"no filter runs every card", LaneParity{}, "c1", "", "pro", FilterRun, ""},
			{"a flash card for a flash friend", flash, "c1", "", "flash", FilterRun, ""},
			{"the tier is read in any case", flash, "c1", "", "Flash", FilterRun, ""},
			{"a pro card for a flash friend is taken back", flash, "c1", "", "pro", FilterTake, "tier pro: she works only flash cards"},
			{"a card with no tier is tier -", flash, "c1", "", "", FilterTake, "tier -:"},
			{"a security stream", security, "c1", "security-v1", "pro", FilterRun, ""},
			{"a security id", security, "sec-12", "", "pro", FilterRun, ""},
			{"neither is skipped", security, "c1", "sprint-v1", "pro", FilterSkip, "card c1 (stream sprint-v1) matches none of security*, sec-*, fp-sec*"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got, why := tc.p.Filter(tc.id, tc.stream, tc.tier)
				assert.Equal(t, tc.want, got)
				assert.Contains(t, why, tc.why)
			})
		}
	})

	t.Run("take back", func(t *testing.T) {
		for _, tc := range []struct {
			card, why string
			want      []string
		}{
			{"c2", "tier pro: she works only flash cards", []string{"friend bob: card c2 is outside her filter: take it back", "nova-sprint friend take bob c2 --reason 'tier pro: she works only flash cards'", "a coordinator verb the sprint server refuses from a friend"}},
			{"c3", "it's mine", []string{`--reason 'it'\''s mine'`}},
		} {
			subject, body := TakeBackText("bob", tc.card, tc.why)
			for _, w := range tc.want {
				assert.Contains(t, subject+"\n"+body, w)
			}
		}
		// in the lanes: a flash friend's pro card is never handed and the coordinator is told
		// once with the take line; a pro card already started is skipped, nobody told
		synctest.Test(t, func(t *testing.T) {
			dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}, {"c3", "queued"}}, []string{"c1", "c2", "c3"}, nil)
			for id, tier := range map[string]string{"c1": "flash", "c2": "pro", "c3": "pro"} {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", id+"~15", "BRIEF.md"), []byte("tier: "+tier+"\nRESULT: "+id+"\n"), 0o644))
			}
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", "c3~15", "repo"), 0o755))
			h := &parityHarness{dir: dir}
			h.turn = finishCard(dir)
			r, _ := parityRig(t, h, 2, &LaneParity{Tiers: []string{"flash"}})
			r.run(t, 20)
			turns := h.got()
			require.Len(t, turns, 1, "only the flash card is handed")
			assert.True(t, strings.HasSuffix(turns[0], ": c1"), turns[0])
			var told []string
			for _, m := range r.adaMessages(t) {
				if strings.Contains(m.Subject, "outside her filter") {
					told = append(told, m.Subject)
					assert.Equal(t, "blocker", m.Kind)
					assert.Contains(t, m.Body, "nova-sprint friend take bob c2 --reason")
				}
			}
			assert.Equal(t, []string{"friend bob: card c2 is outside her filter: take it back"}, told, "told once, and never for the started c3")
			records := strings.Join(r.records, "\n")
			assert.Equal(t, 1, strings.Count(records, "card c2 not handed: take tier pro"), records)
			assert.Contains(t, records, "card c3 not handed: skip tier pro: she works only flash cards; the rest go back to the dealer; started already, so it is not owed back")
		})
	})

	t.Run("generation job name", func(t *testing.T) {
		for _, tc := range []struct {
			card  string
			epoch uint64
			gen   int
			want  string
		}{
			{"c1", 15, 0, "c1~15"},
			{"c1", 15, 1, "c1~15"},
			{"c1", 15, 2, "c1~15.g2"},
			{"opencode-lanes-parity-r2b.w1", 15, 3, "opencode-lanes-parity-r2b.w1~15.g3"},
		} {
			assert.Equal(t, tc.want, JobName(tc.card, tc.epoch, tc.gen))
			id, epoch, gen, ok := ParseJob(tc.want)
			require.True(t, ok)
			assert.Equal(t, tc.card, id)
			assert.Equal(t, int(tc.epoch), epoch)
			assert.Equal(t, max(tc.gen, 1), gen, "ParseJob reads back what JobName names")
			assert.Equal(t, tc.want, heldJob(HeldCard{Card: tc.card, Epoch: tc.epoch, Gen: tc.gen}), "a held card with no job is named by its generation")
		}
		assert.Equal(t, "named", heldJob(HeldCard{Card: "c1", Epoch: 15, Gen: 2, Job: "named"}), "the server's job wins")
		// in the lanes: the row's tier of a later generation's card is found by that name
		synctest.Test(t, func(t *testing.T) {
			dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, nil, nil)
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", "c1~15.g2"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "c1~15.g2", "BRIEF.md"), []byte("RESULT: c1\n"), 0o644))
			q := `{"tasks":[{"id":"c1","state":"queued","gen":2}]}`
			require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(q), 0o644))
			h := &parityHarness{dir: dir}
			r, _ := parityRig(t, h, 1, &LaneParity{Tiers: []string{"flash"}})
			r.d.heldCards = []HeldCard{{Card: "c1", Epoch: 15, Gen: 2, Tier: "pro"}}
			r.run(t, 10)
			assert.Empty(t, h.got(), "the g2 card's row tier is pro: not handed")
			assert.Contains(t, strings.Join(r.records, "\n"), "card c1 not handed: take tier pro")
		})
	})

	t.Run("width under load", func(t *testing.T) {
		for _, tc := range []struct {
			name        string
			p           LaneParity
			width       int
			load        float64
			want        int
			wantHeld    bool
			parse, from string
		}{
			{"no bound", LaneParity{}, 12, 200, 12, false, "", ""},
			{"under the bound", LaneParity{LoadMax: 90}, 12, 90, 12, false, "", ""},
			{"above it: the runner's 3", LaneParity{LoadMax: 90}, 12, 91, 3, true, "", ""},
			{"above it: the flag's width", LaneParity{LoadMax: 90, LoadWidth: 5}, 12, 91, 5, true, "", ""},
			{"never raised by it", LaneParity{LoadMax: 90}, 2, 91, 2, false, "", ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				n, held := tc.p.LoadWidthAt(tc.width, tc.load)
				assert.Equal(t, tc.want, n)
				assert.Equal(t, tc.wantHeld, held)
			})
		}
		for in, want := range map[string]float64{"0.52 0.58 0.59 1/389 12345\n": 0.52, "{ 91.20 80.01 70.00 }\n": 91.2} {
			got, err := LoadOf(in)
			require.NoError(t, err)
			assert.InDelta(t, want, got, 1e-9, in)
		}
		_, err := LoadOf("")
		assert.Error(t, err)
		// in the lanes: three lanes, the load above the bound: one card runs at a time
		synctest.Test(t, func(t *testing.T) {
			dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}, {"c3", "queued"}}, []string{"c1", "c2", "c3"}, nil)
			h := &parityHarness{dir: dir}
			release := make(chan struct{})
			busy, most := 0, 0
			h.turn = func(ctx context.Context, id string) (LaneTurn, error) {
				h.mu.Lock()
				busy++
				most = max(most, busy)
				h.mu.Unlock()
				select {
				case <-release:
				case <-ctx.Done():
				}
				h.mu.Lock()
				busy--
				h.mu.Unlock()
				return finishCard(dir)(ctx, id)
			}
			r, _ := parityRig(t, h, 3, &LaneParity{LoadMax: 5, LoadWidth: 1, Load: func(context.Context) (float64, error) { return 9, nil }})
			r.at[10] = func() { close(release) }
			r.run(t, 30)
			assert.Equal(t, 1, most, "held to one lane while the load is above 5")
			assert.Len(t, h.got(), 3, "every card still runs")
			assert.Contains(t, strings.Join(r.records, "\n"), "load 9.0 above 5.0: lanes held at 1 of 3")
			assert.Contains(t, strings.Join(r.adaGot(t), "\n"), "bob lanes at 1: load 9.0")
		})
	})

	t.Run("token cap HOLD", func(t *testing.T) {
		p := LaneParity{TokenCap: 6_000_000}
		for _, tc := range []struct {
			u    TokenUsage
			over bool
		}{
			{TokenUsage{Input: 5_999_999}, false},
			{TokenUsage{Input: 1_000_000, CacheRead: 4_000_000, Output: 500_000, Reasoning: 500_000}, true},
			{TokenUsage{CacheWrite: 7_000_000}, true},
		} {
			assert.Equal(t, tc.over, p.OverCap(tc.u), "%+v", tc.u)
		}
		assert.False(t, LaneParity{}.OverCap(TokenUsage{Input: 1 << 40}), "no cap, never over")
		report := CapReport("bob", Card{ID: "c1"}, 6_000_000, TokenUsage{Input: 6_000_001}, "$ git push\n")
		assert.True(t, strings.HasPrefix(report, "Verdict: HOLD\nHead: none\n\ntoken cap: 6000001 tokens of a cap of 6000000, last step: $ git push."), report)
		// in the lanes: the card's tokens since it began reach the cap; its REPORT.md is the HOLD,
		// its turn is ended, and the lane finishes it from that report
		synctest.Test(t, func(t *testing.T) {
			dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
			h := &parityHarness{dir: dir}
			usage := &usageFake{by: map[string]TokenUsage{"ses_1": {Input: 9_000_000}}} // the session's earlier cards
			h.turn = func(ctx context.Context, id string) (LaneTurn, error) {
				usage.set("ses_1", TokenUsage{Input: 9_000_000 + 4_000_000, Output: 2_000_000})
				<-ctx.Done() // runs on until the lane is stopped
				return LaneTurn{Exit: -1}, ctx.Err()
			}
			r, state := parityRig(t, h, 1, &LaneParity{TokenCap: 6_000_000, Usage: usage.read})
			r.run(t, 80)
			raw, err := os.ReadFile(filepath.Join(dir, "outbox", "c1~15", "REPORT.md"))
			require.NoError(t, err)
			assert.True(t, strings.HasPrefix(string(raw), "Verdict: HOLD\nHead: none\nCost: unpriced (no route row for - in nova-sprint routes) (opencode: $0.00) tokens input=4000000 cache_read=0 cache_write=0 output=2000000 reasoning=0 "), string(raw))
			assert.Contains(t, string(raw), "\n\ntoken cap: 6000000 tokens of a cap of 6000000, last step: ")
			records := strings.Join(r.records, "\n")
			assert.Contains(t, records, "lane 1: card c1 token cap: 6000000 tokens of a cap of 6000000")
			assert.Contains(t, records, "token_capped=6000000")
			assert.Contains(t, records, "card=done finish=report")
			assert.Equal(t, []string{"ses_1: c1"}, h.got(), "never handed again")
			assert.Empty(t, state.Started)
		})
	})

	t.Run("provider pause", func(t *testing.T) {
		for _, tc := range []struct{ out, want string }{
			{"working\nError: 402 Payment Required\n", "Error: 402 Payment Required"},
			{"\x1b[31mError: \x1b[0mRate limit reached for requests\n", "Error: Rate limit reached for requests"},
			{"Error: insufficient_funds: top up\n", "Error: insufficient_funds: top up"},
			{"Error: 429 too many requests\n", "Error: 429 too many requests"},
			{"the test printed 429 lines\n", ""},
			{"Error: exit status 4290\n", ""},
			{"Error: file not found\n", ""},
		} {
			assert.Equal(t, tc.want, ProviderFailureLine(tc.out), "%q", tc.out)
		}
		subject, body := ProviderStopText("bob", "inception/mercury-2.5", "Error: 402 Payment Required", "/s/LANES-HELD")
		assert.Equal(t, "friend bob: provider failure (inception/mercury-2.5): Error: 402 Payment Required", subject)
		assert.Contains(t, body, "nova-sprint friend down bob --reason 'provider failure (inception/mercury-2.5): Error: 402 Payment Required'")
		assert.Contains(t, body, "remove /s/LANES-HELD")
		// in the lanes: two cards running; one prints a provider failure: both turns are ended,
		// both cards kept (no attempt, not started, not set aside, no report), the lanes held with
		// the exact message in the hold file; removed by a person, both run again and finish
		synctest.Test(t, func(t *testing.T) {
			dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
			hold := filepath.Join(t.TempDir(), "LANES-HELD")
			h := &parityHarness{dir: dir}
			var mu sync.Mutex
			failing := true
			c2 := make(chan struct{})
			h.turn = func(ctx context.Context, id string) (LaneTurn, error) {
				mu.Lock()
				fail := failing
				mu.Unlock()
				if !fail {
					return finishCard(dir)(ctx, id)
				}
				if id == "c2" {
					close(c2)
				}
				if id == "c1" {
					select { // the failure comes once both cards run
					case <-c2:
					case <-ctx.Done():
					}
					tail, _ := ctx.Value(tailKey{}).(func([]byte))
					require.NotNil(t, tail)
					tail([]byte("Error: 402 Payment Required\n"))
				}
				<-ctx.Done()
				return LaneTurn{Exit: -1}, ctx.Err()
			}
			r, state := parityRig(t, h, 2, &LaneParity{StopOnProvider: true, Model: "inception/mercury-2.5", HoldFile: hold})
			var heldTurns []string
			var heldFile string
			var startedAtHold int
			r.at[30] = func() {
				heldTurns = h.got()
				raw, _ := os.ReadFile(hold)
				heldFile = string(raw)
				startedAtHold = len(state.Started)
				mu.Lock()
				failing = false
				mu.Unlock()
				require.NoError(t, os.Remove(hold)) // a person brings the lanes up
			}
			r.run(t, 60)
			require.Len(t, heldTurns, 2, "both ran, and nothing more while held")
			assert.ElementsMatch(t, []string{"c1", "c2"}, []string{heldTurns[0][len(heldTurns[0])-2:], heldTurns[1][len(heldTurns[1])-2:]})
			assert.Contains(t, heldFile, "Error: 402 Payment Required")
			assert.Zero(t, startedAtHold, "a kept card is not started: a restart does not finish it failed")
			records := strings.Join(r.records, "\n")
			assert.Contains(t, records, "provider failure on card c1: Error: 402 Payment Required; every lane stopped (2 running")
			assert.Equal(t, 2, strings.Count(records, "card=kept"), records)
			assert.Contains(t, records, "lanes up: "+hold+" was removed")
			assert.Equal(t, 2, strings.Count(records, "card=done"), records)
			assert.NotContains(t, records, "card=again", "no attempt counted")
			assert.Empty(t, state.GivenUp)
			var stops int
			for _, m := range r.adaMessages(t) {
				if strings.Contains(m.Subject, "provider failure") {
					stops++
					assert.Equal(t, "blocker", m.Kind)
					assert.Contains(t, m.Body, "nova-sprint friend down bob --reason")
				}
			}
			assert.Equal(t, 1, stops, "told once")
		})
		// a turn's own answer, a rate limit (the harness's RateLimited), is the same stop
		synctest.Test(t, func(t *testing.T) {
			dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
			h := &parityHarness{dir: dir}
			h.turn = func(context.Context, string) (LaneTurn, error) {
				return LaneTurn{Exit: 1}, RateLimited{Session: "ses_1", Reason: "429 rate limit reached"}
			}
			r, state := parityRig(t, h, 1, &LaneParity{StopOnProvider: true})
			r.run(t, 30)
			assert.Equal(t, []string{"ses_1: c1"}, h.got(), "held: never handed again")
			assert.Contains(t, strings.Join(r.records, "\n"), `card=kept reason="provider failure: 429 rate limit reached"`)
			assert.Empty(t, state.Started)
			assert.Empty(t, state.GivenUp)
		})
	})

	t.Run("cost line", func(t *testing.T) {
		route := RouteRow{Name: "flash-mercury", Prices: cardcost.Prices{Input: "0.25", CacheRead: "0.025", Output: "1", ReasoningAsOutput: true}}
		u := TokenUsage{Input: 1_000_000, CacheRead: 2_000_000, Output: 100_000, Reasoning: 1, Harness: 0.301}
		for _, tc := range []struct {
			name  string
			route RouteRow
			u     TokenUsage
			want  string
		}{
			{"priced, rounded up to the cent", route, u, "$0.41"}, // 0.25 + 0.05 + 0.100001
			{"exact cents stay", route, TokenUsage{Input: 4_000_000}, "$1.00"},
			{"no route row", RouteRow{}, u, "unpriced (no route row for m/x in nova-sprint routes)"},
			{"a kind with no price", route, TokenUsage{CacheWrite: 1}, "unpriced (route flash-mercury has no cache_write price)"},
			{"reasoning not as output", RouteRow{Name: "r", Prices: cardcost.Prices{Output: "1"}}, TokenUsage{Reasoning: 5}, "$0.00"},
			{"prices this pricer does not apply", RouteRow{Name: "r", Prices: cardcost.Prices{Input: "1", Request: "0.01"}}, u, "unpriced (route r has long-context, request or gateway prices this pricer does not apply)"},
		} {
			t.Run(tc.name, func(t *testing.T) { assert.Equal(t, tc.want, tc.route.Cost("m/x", tc.u)) })
		}
		routes := `{"tiers":[{"name":"flash","routes":[{"name":"flash-other","provider":"inception","model":"other","prices":{"input":"9"}},{"name":"flash-mercury","provider":"inception","model":"mercury-2.5","prices":{"input":"0.25","cache_read":"0.025","output":"1","reasoning_as_output":true}}]}]}`
		got, found, err := RouteOf(routes, "inception/mercury-2.5")
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, route, got)
		_, found, err = RouteOf(routes, "inception/none")
		require.NoError(t, err)
		assert.False(t, found)
		line := CostLine("inception/mercury-2.5", route, u)
		assert.Equal(t, "Cost: $0.41 (opencode: $0.31) tokens input=1000000 cache_read=2000000 cache_write=0 output=100000 reasoning=1 model=inception/mercury-2.5 harness=opencode price_route=flash-mercury", line)
		for _, tc := range []struct{ in, want string }{
			{"Verdict: LAND\nHead: abc\n\nbody\n", "Verdict: LAND\nHead: abc\n" + line + "\n\nbody\n"},
			{"Verdict: HOLD\n**Head:** none", "Verdict: HOLD\n**Head:** none\n" + line + "\n"},
			{"Verdict: FAIL\n", "Verdict: FAIL\n" + line + "\n"},
			{"Verdict: LAND\nHead: abc\nCost: $1.00\n", "Verdict: LAND\nHead: abc\nCost: $1.00\n"},
		} {
			assert.Equal(t, tc.want, WithCost(tc.in, line))
		}
		parsed, err := TokenUsageOf("1000000|2000000|0|100000|1|0.301\n")
		require.NoError(t, err)
		assert.Equal(t, u, parsed)
		_, err = TokenUsageOf("1|2|3\n")
		assert.Error(t, err)
		assert.Contains(t, UsageSQL("ses_'x"), "where id='ses_''x' or parent_id='ses_''x'", "the session and its children, quoted")
		// in the lanes: the card's tokens since it began, priced by the route row the sprint
		// server prints, on REPORT.md under Head: and on RESULT.md, and a note to the coordinator
		synctest.Test(t, func(t *testing.T) {
			dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
			h := &parityHarness{dir: dir}
			usage := &usageFake{by: map[string]TokenUsage{"ses_1": {Input: 500, Harness: 0.5}}}
			h.turn = func(ctx context.Context, id string) (LaneTurn, error) {
				usage.set("ses_1", TokenUsage{Input: 500 + u.Input, CacheRead: u.CacheRead, Output: u.Output, Reasoning: u.Reasoning, Harness: 0.5 + u.Harness})
				return finishCard(dir)(ctx, id)
			}
			r, _ := parityRig(t, h, 1, &LaneParity{Model: "inception/mercury-2.5", Usage: usage.read,
				Routes: func(context.Context) (string, error) { return routes, nil }})
			r.run(t, 20)
			report, err := os.ReadFile(filepath.Join(dir, "outbox", "c1~15", "REPORT.md"))
			require.NoError(t, err)
			assert.Equal(t, "Verdict: LAND\nHead: 0123456789012345678901234567890123456789\n"+line+"\n\nthe gate is green\n", string(report))
			result, err := os.ReadFile(filepath.Join(dir, "outbox", "c1~15", "RESULT.md"))
			require.NoError(t, err)
			assert.Equal(t, "RESULT: c1\n"+line+"\n", string(result))
			assert.Contains(t, strings.Join(r.adaGot(t), "\n"), "bob card c1~15: Verdict: LAND: bob card c1~15: Verdict: LAND\nbob's one-shot lane finished c1~15: Verdict: LAND; cost $0.41 (opencode: $0.31)")
			assert.Contains(t, strings.Join(r.records, "\n"), `cost="$0.41 (opencode: $0.31)" card=done`)
		})
	})

	t.Run("shims", func(t *testing.T) {
		var stderr strings.Builder
		assert.Equal(t, ShimExit, RunShim(&stderr))
		assert.Equal(t, ShimRefusal+"\n", stderr.String())
		for _, tc := range []struct {
			argv0  string
			refuse bool
		}{{"/s/bin/go", true}, {"gofmt", true}, {"/usr/local/bin/nova-friend", false}, {"gopls", false}} {
			assert.Equal(t, tc.refuse, ShimRefuses(tc.argv0), tc.argv0)
		}
		for _, tc := range []struct {
			env  []string
			want []string
		}{
			{[]string{"HOME=/h", "PATH=/usr/bin:/bin", "GOROOT=/usr/local/go"}, []string{"HOME=/h", "PATH=/s/bin" + string(os.PathListSeparator) + "/usr/bin:/bin", "GOROOT=" + NoGoRoot}},
			{[]string{"HOME=/h"}, []string{"HOME=/h", "PATH=/s/bin", "GOROOT=" + NoGoRoot}},
		} {
			assert.Equal(t, tc.want, ShimEnv(tc.env, "/s/bin"))
		}
		dir := filepath.Join(t.TempDir(), "bin")
		made, err := WriteShims(dir, "/opt/nova-friend")
		require.NoError(t, err)
		assert.Equal(t, ShimNames, made)
		for _, n := range ShimNames {
			to, err := os.Readlink(filepath.Join(dir, n))
			require.NoError(t, err)
			assert.Equal(t, "/opt/nova-friend", to)
		}
		made, err = WriteShims(dir, "/opt/nova-friend")
		require.NoError(t, err)
		assert.Empty(t, made, "written once")
		// a command run under a lane turn's context gets the shims first on its PATH
		out, exit, err := RealExec(WithShims(context.Background(), dir), t.TempDir(), "sh", []string{"-c", `echo "$PATH"; echo "$GOROOT"`}, "")
		require.NoError(t, err)
		require.Zero(t, exit)
		lines := strings.Split(strings.TrimSpace(out), "\n")
		require.Len(t, lines, 2)
		assert.True(t, strings.HasPrefix(lines[0], dir+string(os.PathListSeparator)), lines[0])
		assert.Equal(t, NoGoRoot, lines[1])
		// in the lanes: every turn's context carries them
		synctest.Test(t, func(t *testing.T) {
			cards := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
			h := &parityHarness{dir: cards}
			h.turn = finishCard(cards)
			r, _ := parityRig(t, h, 1, &LaneParity{Shims: dir})
			r.run(t, 10)
			h.mu.Lock()
			defer h.mu.Unlock()
			assert.Equal(t, []string{dir}, h.shims)
		})
	})
}
