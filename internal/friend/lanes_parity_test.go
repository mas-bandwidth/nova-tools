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

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The one-shot lanes do what the two runner.zsh stopgaps did (the owner, 2026-10-05: "We need
// to get away from these one shot shell scripts"), each behaviour its own table: the card
// filter, the take back, the generation's job name, the width under load, the token cap's
// HOLD, the provider's hold, the cost line, the shims, and the finish note. The daemon's own
// steps (laneStep, laneDone) run over the rig with fakes: no socket, no wall clock.
func TestOpencodeLanesDoWhatTheRunnerStopgapsDid(t *testing.T) {
	t.Parallel()
	t.Run("filter", testLaneFilter)
	t.Run("take back", testLaneTakeBack)
	t.Run("generation job name", testLaneJobName)
	t.Run("width under load", testLaneWidthUnderLoad)
	t.Run("token cap HOLD", testLaneTokenCap)
	t.Run("provider hold", testLaneProviderHold)
	t.Run("provider lines", testLaneProviderLines)
	t.Run("cost line", testLaneCost)
	t.Run("shims", testLaneShims)
	t.Run("row and install", testLaneRowAndInstall)
}

// The filter is her config's, never code's: tiers she works, and glob patterns over a
// card's stream or id (a security friend's security*, fp-sec*, sec-*, security-*; a flash friend's flash).
func testLaneFilter(t *testing.T) {
	t.Parallel()
	securityOnly := LaneConfig{Cards: []string{"security*", "fp-sec*", "sec-*", "security-*"}}
	flashOnly := LaneConfig{Tiers: []string{"flash"}}
	both := LaneConfig{Tiers: []string{"pro", "heavy"}, Cards: []string{"fg-*"}}
	for _, c := range []struct {
		name string
		cfg  LaneConfig
		card Dealt
		runs bool
		why  string
	}{
		{"no filter runs every card", LaneConfig{}, Dealt{ID: "any", Tier: "heavy"}, true, ""},
		{"a security stream", securityOnly, Dealt{ID: "x1", Stream: "security2"}, true, ""},
		{"an fp-sec id", securityOnly, Dealt{ID: "fp-sec77-f3a", Stream: "s1"}, true, ""},
		{"a sec- id", securityOnly, Dealt{ID: "sec-1", Stream: "s1"}, true, ""},
		{"neither", securityOnly, Dealt{ID: "fix-1", Stream: "s1"}, false, "stream s1 and id fix-1 match none of her cards (security*,fp-sec*,sec-*,security-*)"},
		{"no stream, no match", securityOnly, Dealt{ID: "fix-1"}, false, "stream - and id fix-1 match none"},
		{"flash runs", flashOnly, Dealt{ID: "c1", Tier: "flash"}, true, ""},
		{"pro is not hers", flashOnly, Dealt{ID: "c2", Tier: "pro"}, false, "tier pro is not one she works (flash)"},
		{"an unknown tier is not hers", flashOnly, Dealt{ID: "c3"}, false, "tier - is not one she works"},
		{"tier and pattern both", both, Dealt{ID: "fg-x", Tier: "heavy"}, true, ""},
		{"tier yes, pattern no", both, Dealt{ID: "x", Stream: "s1", Tier: "pro"}, false, "match none of her cards (fg-*)"},
	} {
		runs, why := c.cfg.Works(c.card)
		assert.Equal(t, c.runs, runs, c.name)
		assert.Contains(t, why, c.why, c.name)
		if c.runs {
			assert.Empty(t, why, c.name)
		}
	}

	// in the lanes: a card outside her tiers is never handed, said once; its brief's tier is
	// read when the sprint's answer does not carry the card
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), []byte("RESULT: c1 tier: flash\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "c2~15", "BRIEF.md"), []byte("RESULT: c2 tier: pro\n"), 0o644))
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true, "c2": true}, active: map[string]int{}}
		r, _ := laneRig(t, h, 2)
		r.d.Lanes = func() LaneConfig { return flashOnly }
		r.run(t, 20)
		turns, _, _ := h.got()
		require.Len(t, turns, 1, "only the flash card: %v", turns)
		assert.Contains(t, turns[0], ": c1")
		records := strings.Join(r.records, "\n")
		assert.Equal(t, 1, strings.Count(records, "lanes: card c2 (c2~15) not run: tier pro is not one she works (flash)"), records)
	})
}

// A dealt card outside her filter that she has not started is asked back from the
// coordinator, once, in one blocker carrying the exact friend take line (the server keeps
// friend take the coordinator's); a started one (its jobs/<job> there) stays with her.
func testLaneTakeBack(t *testing.T) {
	t.Parallel()
	cfg := LaneConfig{Tiers: []string{"flash"}}
	dealt := []Dealt{
		{ID: "c1", Tier: "flash", Epoch: 15, Gen: 1},
		{ID: "c2", Tier: "pro", Epoch: 15, Gen: 1},
		{ID: "c3", Tier: "heavy", Epoch: 15, Gen: 2},
		{ID: "c4", Tier: "pro", Epoch: 15, Gen: 1},
	}
	for _, c := range []struct {
		name    string
		started map[string]bool
		asked   map[string]bool
		want    []string
	}{
		{"every unstarted card outside", nil, nil, []string{"c2", "c3", "c4"}},
		{"a started card stays", map[string]bool{"c3~15.g2": true}, nil, []string{"c2", "c4"}},
		{"asked once", nil, map[string]bool{"c2~15": true}, []string{"c3", "c4"}},
	} {
		var ids []string
		for _, tb := range TakeBacks(cfg, dealt, func(d Dealt) bool { return c.started[d.Job()] }, c.asked) {
			ids = append(ids, tb.Card.ID)
		}
		assert.Equal(t, c.want, ids, c.name)
	}
	assert.Empty(t, TakeBacks(LaneConfig{}, dealt, func(Dealt) bool { return false }, nil), "no filter takes nothing back")

	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}, {"c3", "queued"}}, []string{"c1", "c2", "c3"}, nil)
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", "c3~15"), 0o755))
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{}}
		r, _ := laneRig(t, h, 2)
		r.d.Lanes = func() LaneConfig { return cfg }
		reads := 0
		r.d.Dealt = func(context.Context) ([]Dealt, error) {
			reads++
			return []Dealt{{ID: "c1", Tier: "flash", Epoch: 15, Gen: 1}, {ID: "c2", Tier: "pro", Epoch: 15, Gen: 1}, {ID: "c3", Tier: "pro", Epoch: 15, Gen: 1}}, nil
		}
		r.run(t, 20)
		assert.Greater(t, reads, 10, "read each step")
		var asks []bus.Message
		for _, m := range blockers(r.adaMessages(t)) {
			if strings.Contains(m.Subject, "take back") {
				asks = append(asks, m)
			}
		}
		require.Len(t, asks, 1, "asked once")
		assert.Equal(t, bus.KindBlocker, asks[0].Kind)
		assert.Equal(t, "friend bob: take back 1 card(s) outside her filter: c2", asks[0].Subject)
		assert.Contains(t, asks[0].Body, "Run: nova-sprint friend take bob c2 --reason 'outside bob'\\''s filter: her lanes do not run it'")
		assert.NotContains(t, asks[0].Body, "c3", "c3 is started: its jobs/c3~15 is there")
		turns, _, _ := h.got()
		require.NotEmpty(t, turns)
		assert.Contains(t, turns[0], ": c1")
		for _, tn := range turns {
			assert.NotContains(t, tn, "c2", "a card asked back is never run")
		}
	})
}

// The job name carries the generation past the first, as friend sync names the inbox
// directory; the queue's answer gives each card's epoch (its packet's, else the answer's),
// generation, stream and the tier its brief says.
func testLaneJobName(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		id    string
		epoch uint64
		gen   int
		want  string
	}{
		{"c1", 15, 0, "c1~15"},
		{"c1", 15, 1, "c1~15"},
		{"c1", 15, 2, "c1~15.g2"},
		{"fp-sec78-f4.w1", 15, 313, "fp-sec78-f4.w1~15.g313"},
	} {
		assert.Equal(t, c.want, JobName(c.id, c.epoch, c.gen))
	}
	dealt, err := ParseDealt(`{"epoch":15,"cards":[` +
		`{"id":"a","col":"working","stream":"security2","gen":3,"packet":{"epoch":14,"brief":"STATUS: x\nRESULT: a tier: flash\n"}},` +
		`{"id":"b","col":"ready","packet":{"stream":"s1","brief":"no tier"}},` +
		`{"id":"c","col":"ready","gen":1}]}`)
	require.NoError(t, err)
	assert.Equal(t, []Dealt{
		{ID: "a", Col: "working", Stream: "security2", Tier: "flash", Epoch: 14, Gen: 3},
		{ID: "b", Col: "ready", Stream: "s1", Epoch: 15, Gen: 1},
		{ID: "c", Col: "ready", Epoch: 15, Gen: 1},
	}, dealt)
	assert.Equal(t, []string{"a~14.g3", "b~15", "c~15"}, []string{dealt[0].Job(), dealt[1].Job(), dealt[2].Job()})
	_, err = ParseDealt("QUEUE NONE")
	assert.Error(t, err)
	assert.Equal(t, "flash", TierOfBrief("RESULT: x tier: flash\n"))
	assert.Equal(t, "", TierOfBrief("RESULT: x\n"))
}

// At most the row's width at once, held to a lower number while the machine's load is
// above the bound; a load not read holds nothing.
func testLaneWidthUnderLoad(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		cfg   LaneConfig
		width int
		load  float64
		read  bool
		want  int
	}{
		{"off", LaneConfig{}, 12, 200, true, 12},
		{"under the bound", LaneConfig{LoadMax: 90}, 12, 80, true, 12},
		{"at the bound", LaneConfig{LoadMax: 90}, 12, 90, true, 12},
		{"above: the default 3", LaneConfig{LoadMax: 90}, 12, 91, true, 3},
		{"above: her own", LaneConfig{LoadMax: 90, LoadWidth: 5}, 12, 150, true, 5},
		{"never above the row", LaneConfig{LoadMax: 90}, 2, 150, true, 2},
		{"unread", LaneConfig{LoadMax: 90}, 12, 0, false, 12},
	} {
		assert.Equal(t, c.want, c.cfg.LoadCap(c.width, c.load, c.read), c.name)
	}

	synctest.Test(t, func(t *testing.T) {
		var tasks [][2]string
		var ids []string
		for _, id := range []string{"c1", "c2", "c3", "c4", "c5"} {
			tasks, ids = append(tasks, [2]string{id, "queued"}), append(ids, id)
		}
		dir := cardDirFixture(t, tasks, ids, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{}, active: map[string]int{}, block: make(chan struct{})}
		r, _ := laneRig(t, h, 4)
		var mu sync.Mutex
		load := 120.0
		r.d.Lanes = func() LaneConfig { return LaneConfig{LoadMax: 90, LoadWidth: 2} }
		r.d.Load = func() (float64, bool) { mu.Lock(); defer mu.Unlock(); return load, true }
		r.at[10] = func() {
			_, _, seeds := h.got()
			assert.Len(t, seeds, 2, "two lanes while loaded")
			mu.Lock()
			load = 40
			mu.Unlock()
		}
		r.at[20] = func() { close(h.block) }
		r.run(t, 30)
		_, _, seeds := h.got()
		assert.Len(t, seeds, 4, "the row's width once the load falls")
		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "load: 120.0 above 90: new lanes held to 2 of 4")
		assert.Contains(t, records, "load: 40.0 at or below 90: lanes back to 4")
		var held []string
		for _, m := range blockers(r.adaMessages(t)) {
			held = append(held, m.Subject)
		}
		assert.Contains(t, held, "friend bob: lanes held to 2 of 4: load 120.0 above 90")
	})
}

// tokenFake is opencode's database as TokensOf reads it: each session's counts, which a test
// moves on.
type tokenFake struct {
	mu    sync.Mutex
	by    map[string]Tokens
	reads int
	next  func(session string, t Tokens) Tokens // what each read moves the session's counts to
}

func (f *tokenFake) TokensOf(_ context.Context, session string) (Tokens, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	t := f.by[session]
	if f.next != nil {
		f.by[session] = f.next(session, t)
	}
	return t, nil
}

// A card that reaches the token cap: its lane's turn is stopped, the HOLD naming the cap is
// its REPORT.md with the cost under Head:, the card is set aside (never handed again), and
// the coordinator gets the finish note. The cap counts the card's tokens only: the lane's
// session's counts before the card are not its.
func testLaneTokenCap(t *testing.T) {
	t.Parallel()
	report := CapReport("bob", "c1", 6_100_000, 6_000_000, "", "")
	assert.True(t, strings.HasPrefix(report, "Verdict: HOLD\nHead: none\n\ntoken cap: "))
	assert.Contains(t, report, "at 6100000 tokens, at or above her per-card cap of 6000000 tokens")
	assert.NotContains(t, report, "Mercury", "the HOLD names the cap, nothing of one friend's model")

	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c2": true}, active: map[string]int{}, block: make(chan struct{})}
		r, state := laneRig(t, h, 1)
		r.d.Lanes = func() LaneConfig { return LaneConfig{TokenCap: 6_000_000, Model: "inception/mercury-2.5"} }
		// the lane's session counted 9M tokens of earlier cards; c1 adds 2M a read until the
		// cap stops it (reads 1 to 4: its base, then 2M, 4M, 6M), then nothing more
		tf := &tokenFake{by: map[string]Tokens{"ses_1": {Input: 9_000_000}}}
		tf.next = func(_ string, t Tokens) Tokens {
			if tf.reads < 5 {
				t.Input += 1_500_000
				t.Output += 500_000
			}
			return t
		}
		r.d.TokensOf = tf.TokensOf
		r.at[25] = func() { close(h.block) } // c2's turn ends
		r.run(t, 40)
		out := filepath.Join(dir, "outbox", "c1~15")
		raw, err := os.ReadFile(filepath.Join(out, "REPORT.md"))
		require.NoError(t, err)
		lines := strings.Split(string(raw), "\n")
		assert.Equal(t, "Verdict: HOLD", lines[0])
		assert.Equal(t, "Head: none", lines[1])
		assert.True(t, strings.HasPrefix(lines[2], "Cost: unpriced (no route row for inception/mercury-2.5 in nova-sprint routes)"), lines[2])
		assert.Contains(t, string(raw), "token cap: bob's one-shot lane was stopped on card c1 at 6000000 tokens, at or above her per-card cap of 6000000 tokens")
		assert.NoFileExists(t, filepath.Join(out, "RESULT.md"))
		assert.Equal(t, []string{"c1~15"}, state.GivenUp, "set aside: never handed again")
		turns, _, _ := h.got()
		assert.Equal(t, []string{"ses_1: c1", "ses_1: c2"}, turns, "the lane takes the next card")
		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "lane 1: card c1 at 6000000 tokens, the cap is 6000000: the lane's turn is stopped")
		assert.Contains(t, records, "card=capped tokens=6000000 cap=6000000")
		var notes []string
		for _, m := range r.adaMessages(t) {
			notes = append(notes, m.Subject)
		}
		assert.Contains(t, notes, "bob card c1~15: Verdict: HOLD")
	})
}

// A provider out of funds stops every lane at once, keeps each card in its lane's hand,
// writes the exact message to the hold file and stops the beat (her row reads down); a
// restart does not resume her; removing the file (nova-friend resume) does. A rate limit is
// not a hold: it backs off (ratelimit.go, the finding of 2026-10-05, 10:34 AM).
func testLaneProviderHold(t *testing.T) {
	t.Parallel()
	msg := `AI_APICallError: statusCode: 402 Payment Required: insufficient balance`
	synctest.Test(t, func(t *testing.T) {
		r, h, state := limitRig(t, 2, 4)
		hold := filepath.Join(t.TempDir(), HoldFile)
		r.d.HoldPath = hold
		h.setAnswer(func() error { return OutOfFunds{Session: "ses", Reason: msg} })
		steps := 0
		var beatsAtHold, beatsAtResume, turnsAtHold int
		r.d.Pause = func(context.Context, time.Duration) {
			steps++
			switch steps {
			case 30:
				beatsAtHold, turnsAtHold = r.beats, len(h.turnStarts())
				assert.LessOrEqual(t, turnsAtHold, 2, "at most the turns already under way")
				_, found := ReadHold(hold)
				assert.True(t, found, "the hold is written")
			case 60:
				assert.Equal(t, beatsAtHold, r.beats, "no beat while held")
				assert.Equal(t, turnsAtHold, len(h.turnStarts()), "no turn while held")
				h.setAnswer(nil)
				require.NoError(t, os.Remove(hold)) // nova-friend resume
			case 61:
				beatsAtResume = r.beats
			case 120:
				r.cancel()
			}
			synctest.Wait()
		}
		r.run(t, 1<<20)
		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "out of funds: lanes held until a person runs nova-friend resume --as bob ("+hold+"), no beat meanwhile, every lane stopped, every card kept in hand: "+msg)
		assert.Contains(t, records, "resumed: "+hold+" was removed; the lanes start again")
		assert.Greater(t, r.beats, beatsAtResume, "beating again")
		assert.Empty(t, state.GivenUp, "no card set aside by the hold")
		assert.Equal(t, 4, strings.Count(records, " card=done"), "every card done after the resume: %s", records)
		got := blockers(r.adaMessages(t))
		require.Len(t, got, 1, "one judgment: %v", got)
		assert.Contains(t, got[0].Body, "nova-friend resume --as bob")
		assert.Contains(t, got[0].Body, "nova-sprint friend down bob --reason 'out of funds: "+msg+"'")
	})

	// a restart with the hold file there starts nothing and beats nothing
	synctest.Test(t, func(t *testing.T) {
		r, h, _ := limitRig(t, 2, 2)
		hold := filepath.Join(t.TempDir(), HoldFile)
		require.NoError(t, WriteHold(hold, "2026-10-05T15:00:00Z "+msg))
		r.d.HoldPath = hold
		steps := 0
		r.d.Pause = func(context.Context, time.Duration) {
			if steps++; steps == 30 {
				r.cancel()
			}
			synctest.Wait()
		}
		r.run(t, 1<<20)
		assert.Empty(t, h.turnStarts(), "no turn")
		_, _, seeds := h.got()
		assert.Empty(t, seeds, "no session opened")
		assert.Zero(t, r.beats, "no beat: her row reads down")
		assert.Equal(t, "held: 2026-10-05T15:00:00Z "+msg, r.last().BeatError)
		assert.Contains(t, strings.Join(r.records, "\n"), "held: "+hold+" holds the lanes from before the restart")
		got, found := ReadHold(hold)
		assert.True(t, found)
		assert.Equal(t, "2026-10-05T15:00:00Z "+msg, got)
	})
}

// Only the harness's error lines are read for a provider's limit: a card whose own text
// talks of rate limits or a 402 pauses nothing (the runner matched only ^Error: lines).
func testLaneProviderLines(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		out  string
		want string // "rate", "funds" or ""
	}{
		{"Error: Rate limit reached for model\n", "rate"},
		{"AI_APICallError: statusCode: 429\n", "rate"},
		{`{"type":"error","error":{"type":"rate_limit_error"}}` + "\n", "rate"},
		{"Error: insufficient balance\n", "funds"},
		{"AI_APICallError: statusCode: 402 Payment Required\n", "funds"},
		{"I added a backoff for 429 Too Many Requests and a rate limit reached test.\n", ""},
		{"The card's report: a 402 Payment Required now holds the lanes (out of funds).\n", ""},
		{"  Error handling covers rate limit exceeded\n", ""},
		{"wrote ratelimit.go: insufficient balance is out of funds\n", ""},
	} {
		err := ProviderLimit("ses_1", c.out)
		var rate RateLimited
		var funds OutOfFunds
		switch c.want {
		case "rate":
			assert.ErrorAs(t, err, &rate, c.out)
		case "funds":
			assert.ErrorAs(t, err, &funds, c.out)
		default:
			assert.NoError(t, err, c.out)
		}
	}
}

// The card's tokens are its lane session's (and children's) beyond what the session counted
// before the card, priced by the route row of her model, rounded up to the cent; with no row
// the cost is unpriced with its reason. The Cost: line goes under Head: on REPORT.md (a
// REPORT.draft.md published as it), RESULT.md gets tokens: and cost:, and the coordinator a
// finish note.
func testLaneCost(t *testing.T) {
	t.Parallel()
	sheet := cardcost.Prices{Input: "0.25", CacheRead: "0.025", Output: "0.75", ReasoningAsOutput: true}
	tk := Tokens{Input: 1_000_000, Output: 200_000, Reasoning: 100_000, Cost: 0.401}
	for _, c := range []struct {
		name  string
		t     Tokens
		route Route
		found bool
		want  string
	}{
		{"priced, rounded up", tk, Route{Name: "flash-mercury", Prices: sheet}, true, "$0.48"}, // 0.25 + 0.3*0.75 = 0.475
		{"reasoning not output", tk, Route{Name: "r", Prices: cardcost.Prices{Input: "0.25", Output: "0.75"}}, true, "$0.40"},
		{"no row", tk, Route{}, false, "unpriced (no route row for inception/mercury-2.5 in nova-sprint routes)"},
		{"a class with no price", Tokens{CacheWrite: 10}, Route{Name: "r", Prices: sheet}, true, "unpriced (route r: no-price:cache_write)"},
		{"long context", tk, Route{Name: "r", Prices: cardcost.Prices{Input: "1", Output: "1", LongContext: 200_000}}, true, "unpriced (route r has a long-context price"},
	} {
		assert.True(t, strings.HasPrefix(CostOf(c.t, c.route, c.found, "inception/mercury-2.5"), c.want), "%s: %s", c.name, CostOf(c.t, c.route, c.found, "inception/mercury-2.5"))
	}
	assert.Equal(t, "Cost: $0.48 (opencode: $0.41) tokens input=1000000 cache_read=0 cache_write=0 output=200000 reasoning=100000 model=inception/mercury-2.5 harness=opencode price_route=flash-mercury",
		CostLine("$0.48", tk, "inception/mercury-2.5", "flash-mercury"))
	for _, c := range []struct{ in, want string }{
		{"Verdict: LAND\nHead: abc\n\nwords\n", "Verdict: LAND\nHead: abc\nCost: X\n\nwords\n"},
		{"Verdict: LAND\n**Head:** abc\n", "Verdict: LAND\n**Head:** abc\nCost: X\n"},
		{"Verdict: FAIL\n", "Verdict: FAIL\nCost: X\n"},
		{"Verdict: LAND\nHead: abc\nCost: old\n", "Verdict: LAND\nHead: abc\nCost: old\n"},
	} {
		assert.Equal(t, c.want, WithCost(c.in, "Cost: X"))
	}
	routes := `{"tiers":"x","routes":[{"route":{"name":"pro-x","provider":"p","model":"x","prices":{}}},` +
		`{"route":{"name":"flash-mercury","provider":"inception","model":"mercury-2.5","prices":{"input":"0.25","output":"0.75","reasoning_as_output":true}}}]}`
	r, found, err := FindRoute(routes, "inception/mercury-2.5")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, Route{Name: "flash-mercury", Prices: cardcost.Prices{Input: "0.25", Output: "0.75", ReasoningAsOutput: true}}, r)
	_, found, err = FindRoute(routes, "inception/mercury-3")
	assert.NoError(t, err)
	assert.False(t, found)
	q, err := SessionTokensSQL("ses_1abc")
	require.NoError(t, err)
	assert.Contains(t, q, "where id = 'ses_1abc' or parent_id = 'ses_1abc';")
	_, err = SessionTokensSQL("x' or 1=1 --")
	assert.Error(t, err, "nothing but an id goes into the query")
	got, err := ParseSessionTokens("10\t2\t3\t4\t5\t0.25\t2\n")
	require.NoError(t, err)
	assert.Equal(t, Tokens{Input: 10, CacheRead: 2, CacheWrite: 3, Output: 4, Reasoning: 5, Cost: 0.25, Sessions: 2}, got)
	var ran []string
	got, err = SessionTokens(context.Background(), func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
		ran = append([]string{name}, args...)
		return "1|0|0|2|0|0.5|1\n", 0, nil
	}, "/h/opencode.db", "ses_1")
	require.NoError(t, err)
	assert.Equal(t, int64(3), got.Total())
	assert.Equal(t, []string{"sqlite3", "-readonly", "-tabs", "/h/opencode.db"}, ran[:4])

	// in the lanes: laneDone publishes it at the card's finish
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		out := filepath.Join(dir, "outbox", "c1~15")
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{},
			draft: map[string]string{"c1": "Verdict: LAND\nHead: 0123abcd\n\ngate green\n"}}
		r, _ := laneRig(t, h, 1)
		r.d.Lanes = func() LaneConfig { return LaneConfig{Model: "inception/mercury-2.5"} }
		base := Tokens{Input: 5_000_000, Output: 70_000, Cost: 3}
		after := Tokens{Input: base.Input + tk.Input, Output: base.Output + tk.Output, Reasoning: tk.Reasoning, Cost: base.Cost + tk.Cost}
		tf := &tokenFake{by: map[string]Tokens{"ses_1": base}, next: func(string, Tokens) Tokens { return after }}
		r.d.TokensOf = tf.TokensOf
		r.d.Route = func(_ context.Context, model string) (Route, bool, error) {
			return FindRoute(routes, model)
		}
		r.run(t, 10)
		raw, err := os.ReadFile(filepath.Join(out, "REPORT.md"))
		require.NoError(t, err)
		assert.Equal(t, "Verdict: LAND\nHead: 0123abcd\n"+
			"Cost: $0.48 (opencode: $0.41) tokens input=1000000 cache_read=0 cache_write=0 output=200000 reasoning=100000 model=inception/mercury-2.5 harness=opencode price_route=flash-mercury\n\ngate green\n", string(raw))
		assert.NoFileExists(t, filepath.Join(out, ReportDraft), "the draft is published")
		res, err := os.ReadFile(filepath.Join(out, "RESULT.md"))
		require.NoError(t, err)
		assert.Equal(t, "RESULT: c1\ntokens: input=1000000 cache_read=0 cache_write=0 output=200000 reasoning=100000 model=inception/mercury-2.5 harness=opencode\ncost: $0.48 (opencode: $0.41)\n", string(res))
		var note *bus.Message
		for _, m := range r.adaMessages(t) {
			if strings.HasPrefix(m.Subject, "bob card ") {
				note = &m
			}
		}
		require.NotNil(t, note, "a finish note to the coordinator")
		assert.Equal(t, "bob card c1~15: Verdict: LAND", note.Subject)
		assert.Contains(t, note.Body, "bob's one-shot lane finished c1~15: Verdict: LAND; cost $0.48; wall ")
	})
}

// go and gofmt on a lane's PATH are this binary, which refuses by those names, and GOROOT
// points nowhere: every lane child (and only a lane's) runs through env with them first.
func testLaneShims(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	self := filepath.Join(t.TempDir(), "nova-friend")
	require.NoError(t, os.WriteFile(self, []byte("binary"), 0o755))
	bin, err := WriteShims(state, self)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(state, ShimDir), bin)
	for _, name := range []string{"go", "gofmt"} {
		to, err := os.Readlink(filepath.Join(bin, name))
		require.NoError(t, err)
		assert.Equal(t, self, to)
		assert.True(t, IsShim(filepath.Join(bin, name)))
	}
	_, err = WriteShims(state, self)
	require.NoError(t, err, "written again, the same")
	assert.False(t, IsShim("/usr/local/bin/nova-friend"))
	assert.Equal(t, "GO REFUSED go: go never runs on this machine; sync the clone to bench-a,bench-b and run it there over ssh: rsync -a --delete jobs/<job>/repo/ <bench>:<dir>/ && ssh <bench> 'cd <dir> && go ...'\n",
		RefuseGo("go", "bench-a,bench-b"))

	env := ShimEnv(bin, "/usr/bin:/bin", "bench-a")
	assert.Equal(t, []string{"PATH=" + bin + ":/usr/bin:/bin", "GOROOT=" + NoGoRoot, GoBenchEnv + "=bench-a"}, env)
	for _, c := range []struct {
		name string
		ctx  context.Context
		env  []string
		want []string
	}{
		{"a lane's child", LaneContext(context.Background()), env, append(append([]string{"env"}, env...), "opencode", "run", "--session", "ses_1")},
		{"the daemon's own", context.Background(), env, []string{"opencode", "run", "--session", "ses_1"}},
		{"no shims", LaneContext(context.Background()), nil, []string{"opencode", "run", "--session", "ses_1"}},
	} {
		var got []string
		run := ShimExec(c.env, func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
			got = append([]string{name}, args...)
			return "", 0, nil
		})
		_, _, err := run(c.ctx, "/d", "opencode", []string{"run", "--session", "ses_1"}, "")
		require.NoError(t, err)
		assert.Equal(t, c.want, got, c.name)
	}
	// through the wall: the lane's wall runs env, which runs the harness
	var walled []string
	wall := Wall{Self: []string{"/bin/nova-friend"}, Dir: "/d", Deny: []string{"~/self"}}
	run := ShimExec(env, wall.Exec(func(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
		walled = append([]string{name}, args...)
		return "", 0, nil
	}))
	_, _, err = run(LaneContext(context.Background()), "/d", "opencode", []string{"run"}, "")
	require.NoError(t, err)
	assert.Equal(t, "/bin/nova-friend", walled[0])
	assert.Equal(t, append(append([]string{"--", "env"}, env...), "opencode", "run"), walled[len(walled)-len(env)-4:])
}

// The lanes' config is her row's when her beat carries it (row_tiers=, row_cards=,
// row_load_max=, row_load_width=, row_token_cap=, row_model=), else the flags'; install
// writes the flags into her agent.
func testLaneRowAndInstall(t *testing.T) {
	t.Parallel()
	flags := LaneConfig{Tiers: []string{"flash"}, TokenCap: 6_000_000, Model: "inception/mercury-2.5"}
	for _, c := range []struct {
		answer string
		want   LaneConfig
	}{
		{"FRIEND-BEAT OK bob row_mode=one-shot row_width=12", flags},
		{"FRIEND-BEAT OK ada row_cards=security*,fp-sec* row_tiers=pro,heavy row_load_max=90 row_load_width=2 row_token_cap=0 row_model=p/m",
			LaneConfig{Tiers: []string{"pro", "heavy"}, Cards: []string{"security*", "fp-sec*"}, LoadMax: 90, LoadWidth: 2, Model: "p/m"}},
		{"row_load_max=lots row_token_cap=-1", flags},
	} {
		assert.Equal(t, c.want, ParseLaneRow(c.answer, flags), c.answer)
	}
	a := Agent{Friend: "bob", Harness: "opencode", Dir: "/w", Binary: "/b/nova-friend", Redis: "r:1", Server: "s:2", Width: 12,
		LaneArgs: []string{"--tiers", "flash", "--token-cap", "6000000", "--go-bench", "bench-a,bench-b"}}
	args := a.Args()
	assert.Equal(t, []string{"--tiers", "flash", "--token-cap", "6000000", "--go-bench", "bench-a,bench-b"}, args[len(args)-6:])
}
