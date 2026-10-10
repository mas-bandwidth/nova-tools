package friend

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A rate limit is not out of funds (the finding of 2026-10-05, 10:34 AM: a
// friend at 24 lanes hit his provider's input-token rate limit, the runner
// held him down as if out of funds and returned 20 cards, and a person
// cleared the pause by hand). A rate limit passes in a minute; out of funds
// does not.

// limitHarness is a lanes harness whose card turns answer what answer says
// for the turn about to run (nil: the lanes harness's own turn), and which
// keeps every turn's start on the rig's clock.
type limitHarness struct {
	*lanesHarness
	mu     sync.Mutex
	now    func() time.Time
	answer func() error
	starts []time.Time
}

func (h *limitHarness) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
	h.mu.Lock()
	h.starts = append(h.starts, h.now())
	answer := h.answer
	h.mu.Unlock()
	if answer != nil {
		if err := answer(); err != nil {
			h.lanesHarness.mu.Lock()
			h.turns = append(h.turns, session+": "+cardOfText.FindStringSubmatch(text)[1])
			h.lanesHarness.mu.Unlock()
			return LaneTurn{Exit: 1}, err
		}
	}
	return h.lanesHarness.DeliverTo(ctx, session, text)
}

func (h *limitHarness) turnStarts() []time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]time.Time(nil), h.starts...)
}

func (h *limitHarness) setAnswer(f func() error) {
	h.mu.Lock()
	h.answer = f
	h.mu.Unlock()
}

// limitRig is the lane rig over a limit harness, at width lanes, with cards
// c1..cn queued, delivered and finished by their turn.
func limitRig(t *testing.T, width, cards int) (*rig, *limitHarness, *LaneState) {
	var tasks [][2]string
	var ids []string
	finish := map[string]bool{}
	for i := 1; i <= cards; i++ {
		id := fmt.Sprintf("c%d", i)
		tasks, ids, finish[id] = append(tasks, [2]string{id, "queued"}), append(ids, id), true
	}
	dir := cardDirFixture(t, tasks, ids, nil)
	lh := &lanesHarness{dir: dir, finish: finish, active: map[string]int{}}
	r, state := laneRig(t, lh, width)
	h := &limitHarness{lanesHarness: lh}
	h.now = func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now }
	r.d.Deliver = h
	return r, h, state
}

// adaMessages is every message on the coordinator's stream.
func (r *rig) adaMessages(t *testing.T) []bus.Message {
	t.Helper()
	got, err := r.store.Range(context.Background(), bus.StreamOf("ada"), "-", "+", 100)
	require.NoError(t, err)
	var out []bus.Message
	for _, e := range got {
		out = append(out, e.Message())
	}
	return out
}

// A 429 pauses new lanes for a backoff (30 s, doubling) and lowers the live
// lane cap by a quarter; the card stays in its lane's hand, counted toward
// nothing, never set aside; the lanes resume after the backoff with no hold
// (the beat never stops, nothing waits on a person); three lowerings in an
// hour are one judgment to the coordinator; and a clean ten minutes, measured
// by a clean turn, raises the cap one lane.
func TestARateLimitBacksOffAndResumesWithoutAHold(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r, h, state := limitRig(t, 4, 6)
		limited := true
		var mu sync.Mutex
		h.setAnswer(func() error {
			mu.Lock()
			defer mu.Unlock()
			if limited {
				return RateLimited{Session: "ses", Reason: "429 Too Many Requests: input token limit exceeded"}
			}
			return nil
		})
		// the third pause is two minutes; the provider is clean again during it
		r.at[150] = func() { mu.Lock(); limited = false; mu.Unlock() }
		r.run(t, 900)

		records := strings.Join(r.records, "\n")
		lines := func(word string) []string {
			var out []string
			for _, l := range r.records {
				if strings.Contains(l, word) {
					out = append(out, l)
				}
			}
			return out
		}
		lowered := lines("rate limit: lanes paused")
		require.Len(t, lowered, 3, "three episodes, one line each, however many lanes met each:\n%s", records)
		assert.Contains(t, lowered[0], "paused 30s")
		assert.Contains(t, lowered[0], "cap 4 -> 3 of 4")
		assert.Contains(t, lowered[1], "paused 1m0s")
		assert.Contains(t, lowered[1], "cap 3 -> 2 of 4")
		assert.Contains(t, lowered[2], "paused 2m0s")
		assert.Contains(t, lowered[2], "cap 2 -> 1 of 4")
		assert.Len(t, lines("rate limit: lanes resume"), 3, "each pause's end is one line")
		raised := lines("rate limit: cap raised")
		require.Len(t, raised, 1, "one clean ten minutes so far:\n%s", records)
		assert.Contains(t, raised[0], "cap raised 1 -> 2 of 4 after a clean 10m0s")

		// no turn started inside a pause
		var pauses [][2]time.Time
		for _, l := range r.records {
			if !strings.Contains(l, "rate limit: lanes paused") {
				continue
			}
			at, err := time.Parse(time.RFC3339, strings.Fields(l)[0])
			require.NoError(t, err)
			_, until, _ := strings.Cut(l, " until ")
			end, err := time.Parse(time.RFC3339, strings.TrimSuffix(strings.Fields(until)[0], ","))
			require.NoError(t, err)
			pauses = append(pauses, [2]time.Time{at, end})
		}
		for _, s := range h.turnStarts() {
			for _, p := range pauses {
				assert.False(t, s.After(p[0]) && s.Before(p[1]), "a turn started at %s inside the pause %s..%s", s, p[0], p[1])
			}
		}

		// the cards: none set aside, none handed "again", every one done
		assert.Empty(t, state.GivenUp, "a rate limit never sets a card aside")
		assert.NotContains(t, records, "card=again", "a rate-limited turn counts toward nothing")
		assert.Contains(t, records, "card=kept")
		assert.Equal(t, 6, strings.Count(records, " card=done"), records)

		// no hold: every step beat, the session is ok, and the one message is the judgment
		assert.Equal(t, 900, r.beats, "the beat never stopped")
		assert.Equal(t, SessionOK, r.last().Session)
		got := r.adaMessages(t)
		require.Len(t, got, 1, "one judgment, nothing else: %v", got)
		assert.Equal(t, bus.KindBlocker, got[0].Kind)
		assert.Contains(t, got[0].Subject, "friend bob: lane cap lowered 3 times in 1h0m0s by rate limits")
		assert.Contains(t, got[0].Body, "nova-config friend set bob --width")
	})
}

// Out of funds is not a rate limit: a 402 holds the lanes (no new turn, no
// open), keeps the card in hand, and tells the coordinator once, a judgment.
func TestOutOfFundsHoldsTheLanesWithOneJudgment(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r, h, state := limitRig(t, 2, 4)
		h.setAnswer(func() error { return OutOfFunds{Session: "ses", Reason: "402 Payment Required: insufficient balance"} })
		var heldAt int
		r.at[20] = func() { heldAt = len(h.turnStarts()) }
		r.run(t, 200)
		records := strings.Join(r.records, "\n")
		assert.Equal(t, 1, strings.Count(records, "out of funds: lanes held"), records)
		assert.Equal(t, heldAt, len(h.turnStarts()), "no turn after the hold")
		assert.LessOrEqual(t, heldAt, 2, "at most the turns already under way")
		assert.Empty(t, state.GivenUp)
		assert.NotContains(t, records, "card=again")
		assert.NotContains(t, records, "rate limit:", "out of funds lowers no cap")
		got := r.adaMessages(t)
		require.Len(t, got, 1, "one judgment: %v", got)
		assert.Equal(t, bus.KindBlocker, got[0].Kind)
		assert.Contains(t, got[0].Subject, "friend bob: out of funds")
		assert.Contains(t, got[0].Body, "nova-sprint friend down bob")
		assert.Contains(t, r.last().Lanes, ":held")
	})
}

// The governor alone: the backoff doubles from 30 s to its ceiling of 10
// minutes; lanes that met the same episode lower the cap once; a clean ten
// minutes with a clean turn raises one lane, and none without a turn; back
// at the row's width the backoff starts over.
func TestTheLaneGovernorBacksOffAndRaisesByMeasurement(t *testing.T) {
	t.Parallel()
	zeroJitter := func() float64 { return 0.5 }
	g := &LaneGovernor{Rand: zeroJitter}
	now := t0
	var pauses []time.Duration
	for i := 0; i < 7; i++ {
		started := now.Add(-time.Second)
		line, _ := g.RateLimit(now, started, 24, "429")
		require.NotEmpty(t, line)
		pauses = append(pauses, g.pausedUntil.Sub(now))
		again, _ := g.RateLimit(now.Add(time.Second), started, 24, "429") // another lane, the same episode
		assert.Empty(t, again, "a turn started before the lowering is the same episode")
		now = g.pausedUntil
	}
	assert.Equal(t, []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute, 10 * time.Minute}, pauses)
	assert.Equal(t, 5, g.Cap(24), "24, 18, 14, 11, 9, 7, 6, 5: a quarter off each, at least one")

	g = &LaneGovernor{Rand: zeroJitter}
	now = t0
	_, _ = g.RateLimit(now, now.Add(-time.Second), 8, "429")
	assert.Equal(t, 6, g.Cap(8))
	assert.True(t, g.Paused(now.Add(29*time.Second)))
	assert.NotEmpty(t, g.Step(now.Add(30*time.Second), 8), "the resume line")
	assert.False(t, g.Paused(now.Add(30*time.Second)))
	assert.Empty(t, g.Step(now.Add(41*time.Minute), 8), "no clean turn measured: no raise")
	g.Clean(now.Add(41 * time.Minute))
	assert.NotEmpty(t, g.Step(now.Add(41*time.Minute), 8))
	assert.Equal(t, 7, g.Cap(8))
	g.Clean(now.Add(45 * time.Minute))
	assert.Empty(t, g.Step(now.Add(50*time.Minute), 8), "ten minutes from the last raise")
	assert.NotEmpty(t, g.Step(now.Add(51*time.Minute), 8))
	assert.Equal(t, 8, g.Cap(8), "back at the row's width")
	assert.Equal(t, 12, g.Cap(12), "the row's width again, whatever it becomes")
	_, _ = g.RateLimit(now.Add(52*time.Minute), now.Add(51*time.Minute), 12, "429")
	assert.Equal(t, now.Add(52*time.Minute+30*time.Second), g.pausedUntil, "the backoff starts over at full width")
	assert.Equal(t, 9, g.Cap(12))
	assert.Equal(t, 4, g.Cap(4), "never above the row")

	// three lowerings in an hour are one judgment; the next three another
	g = &LaneGovernor{Rand: zeroJitter}
	now = t0
	var judged []bool
	for i := 0; i < 6; i++ {
		_, j := g.RateLimit(now, now.Add(-time.Second), 24, "429")
		judged = append(judged, j)
		now = now.Add(5 * time.Minute)
	}
	assert.Equal(t, []bool{false, false, true, false, false, true}, judged)
	g = &LaneGovernor{Rand: zeroJitter}
	_, _ = g.RateLimit(t0, t0.Add(-time.Second), 24, "429")
	_, _ = g.RateLimit(t0.Add(40*time.Minute), t0.Add(39*time.Minute), 24, "429")
	_, j := g.RateLimit(t0.Add(61*time.Minute), t0.Add(60*time.Minute), 24, "429")
	assert.False(t, j, "the first lowering is out of the hour")

	assert.True(t, g.Hold("402"), "the first hold")
	assert.False(t, g.Hold("402"), "said once")
	assert.True(t, g.Paused(t0.Add(48*time.Hour)), "held until the daemon restarts")
}

// The words: a rate limit (429, "rate limit reached", "too many requests",
// "input token limit exceeded") and out of funds (402, insufficient balance
// or credits) in a turn's tail, out of funds first; a usage limit, the
// harness's own (Limits), is neither.
func TestProviderLimitTellsARateLimitFromOutOfFunds(t *testing.T) {
	t.Parallel()
	rate := []string{
		`AI_APICallError: statusCode: 429`,
		`Error: Rate limit reached for model in organization on tokens per min (TPM). Please try again in 1.2s.`,
		`HTTP 429 Too Many Requests`,
		`{"error":{"code":429,"message":"Provider returned error"}}`,
		`Error: input token limit exceeded for this minute`,
		`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`,
		`too many requests, retry later`,
	}
	for _, out := range rate {
		err := ProviderLimit("ses_1", "working...\n"+out+"\n")
		var rl RateLimited
		assert.ErrorAs(t, err, &rl, out)
		assert.Equal(t, "ses_1", rl.Session)
		assert.NotEmpty(t, rl.Reason)
	}
	funds := []string{
		`{"error":{"code":402,"message":"Insufficient credits. Add more using https://openrouter.test/settings/credits"}}`,
		`AI_APICallError: statusCode: 402 Payment Required`,
		`Error: insufficient balance`,
		`429 rate limit; insufficient balance on the account`,
	}
	for _, out := range funds {
		var oof OutOfFunds
		assert.ErrorAs(t, ProviderLimit("ses_1", out), &oof, out)
	}
	for _, out := range []string{
		"", "all done\n",
		"You've hit your usage limit. Try again at 6:52 PM",
		"line 429: an ordinary line\n",
		strings.Repeat("x", 3*LimitTail) + "\n",
		"429 Too Many Requests\n" + strings.Repeat("the reply went on\n", 200),
	} {
		assert.NoError(t, ProviderLimit("ses_1", out), out)
	}
}

// The OpenCode lanes answer a rate limit and out of funds as such, from the
// turn's output, before a provider's refusal.
func TestOpenCodeLanesAnswerARateLimitAndOutOfFunds(t *testing.T) {
	t.Parallel()
	out := ""
	run := func(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
		if args[0] == "session" {
			return "[]", 0, nil
		}
		return out, 1, nil
	}
	o := &OpenCode{Dir: t.TempDir(), Run: run}
	out = `{"type":"error","error":{"type":"invalid_request_error","message":"input token limit exceeded"}}` + "\n"
	_, err := o.DeliverTo(context.Background(), "ses_1", "card")
	var rl RateLimited
	require.ErrorAs(t, err, &rl, "a rate limit, not a refusal of the session")
	out = "AI_APICallError: statusCode: 402 insufficient balance\n"
	_, err = o.DeliverTo(context.Background(), "ses_1", "card")
	var oof OutOfFunds
	require.ErrorAs(t, err, &oof)
	_, err = o.OpenSession(context.Background(), "seed")
	require.ErrorAs(t, err, &oof, "an open meets it too")
	out = `{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}` + "\n"
	_, err = o.DeliverTo(context.Background(), "ses_1", "card")
	var pr ProviderRefused
	require.ErrorAs(t, err, &pr, "a refusal is still one")
}

// With an injectable fixed source, two lane governors paused at the same
// instant for the same pause resume at different times, each within
// [0.8, 1.2] of the pause; a PauseUntil reset never resumes before the reset
// (docs/SPEC-FRIEND.md #rate-limit-backs-off-not-down.w1).
func TestALaneResumeAfterARateLimitIsJittered(t *testing.T) {
	t.Parallel()
	src := rand.New(rand.NewPCG(12345, 67890))
	g1 := &LaneGovernor{Rand: src.Float64}
	g2 := &LaneGovernor{Rand: src.Float64}

	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	started := now.Add(-time.Second)

	l1, _ := g1.RateLimit(now, started, 8, "429")
	l2, _ := g2.RateLimit(now, started, 8, "429")

	require.NotEmpty(t, l1)
	require.NotEmpty(t, l2)
	assert.NotEqual(t, g1.pausedUntil, g2.pausedUntil, "two governors paused at the same instant resume at different times")

	p1 := g1.pausedUntil.Sub(now)
	p2 := g2.pausedUntil.Sub(now)

	assert.GreaterOrEqual(t, p1, time.Duration(float64(RateBackoffFirst)*0.8))
	assert.LessOrEqual(t, p1, time.Duration(float64(RateBackoffFirst)*1.2))
	assert.GreaterOrEqual(t, p2, time.Duration(float64(RateBackoffFirst)*0.8))
	assert.LessOrEqual(t, p2, time.Duration(float64(RateBackoffFirst)*1.2))

	// PauseUntil reset never resumes before the reset, and jitter adds up to 20%
	for i := 1; i <= 10; i++ {
		g := &LaneGovernor{Rand: src.Float64}
		reset := now.Add(time.Duration(i) * 5 * time.Minute)
		g.PauseUntil(reset, "usage limit", now)
		assert.False(t, g.pausedUntil.Before(reset), "a PauseUntil reset never resumes before the reset")
		assert.GreaterOrEqual(t, g.pausedUntil, reset)
		remaining := reset.Sub(now)
		maxAllowed := reset.Add(time.Duration(float64(remaining) * 0.20))
		assert.LessOrEqual(t, g.pausedUntil, maxAllowed)
	}
}
