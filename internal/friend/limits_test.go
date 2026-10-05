package friend

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixture is the lines of testdata/<name>.txt, one text each.
func fixture(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".txt")
	require.NoError(t, err)
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func TestEachHarnessLimitAndCreditsTextParsesWithItsReset(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, edt)
	rest := time.Hour
	// each fixture's lines in order: the kind and the reset each reads to
	want := map[string][][2]string{
		"claude":      {{LimitKindLimit, "2026-10-04T11:00:00-04:00"}, {LimitKindCredits, "2026-10-04T10:00:00-04:00"}},
		"codex":       {{LimitKindLimit, "2026-10-04T09:15:00-04:00"}, {LimitKindCredits, "2026-10-04T10:00:00-04:00"}},
		"opencode":    {{LimitKindLimit, "2026-10-04T09:10:00-04:00"}, {LimitKindCredits, "2026-10-04T18:52:00-04:00"}},
		"grok":        {{LimitKindLimit, "2026-10-04T18:30:00-04:00"}, {LimitKindCredits, "2026-10-04T10:00:00-04:00"}},
		"antigravity": {{LimitKindLimit, "2026-10-04T11:00:00-04:00"}, {LimitKindCredits, "2026-10-04T10:00:00-04:00"}},
		"dsh":         {{LimitKindLimit, "2026-10-04T09:30:00-04:00"}, {LimitKindCredits, "2026-10-04T10:00:00-04:00"}},
		"gemini":      {{LimitKindLimit, "2026-10-04T10:00:00-04:00"}, {LimitKindCredits, "2026-10-04T10:00:00-04:00"}},
	}
	require.Len(t, want, len(harnessLimitWords), "every harness with a parser has its fixture")
	for harness, rows := range want {
		lines := fixture(t, harness)
		require.Len(t, lines, len(rows), harness)
		for i, line := range lines {
			hl, found := ReadHarnessLimit(harness, "the turn failed\n"+line+"\n", now, rest)
			require.True(t, found, "%s: %s", harness, line)
			assert.Equal(t, rows[i][0], hl.Kind, "%s: %s", harness, line)
			assert.Equal(t, rows[i][1], hl.Until.In(edt).Format(time.RFC3339), "%s: %s", harness, line)
			assert.NotEmpty(t, hl.Reason)
		}
	}
	hl, found := ReadHarnessLimit("dsh", "HTTP 402: insufficient balance on account.\n", now, 0)
	require.True(t, found)
	assert.Equal(t, now.Add(DefaultLimitWait), hl.Until, "no reset and no --limit-rest: the default hour")
}

func TestAnUnrecognisedTextStaysAnOrdinaryFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, edt)
	for harness := range harnessLimitWords {
		for _, line := range fixture(t, "unrecognized") {
			_, found := ReadHarnessLimit(harness, line+"\n", now, time.Hour)
			assert.False(t, found, "%s: %s", harness, line)
		}
	}
	_, found := ReadHarnessLimit("fake", "HTTP 402: insufficient balance\n", now, time.Hour)
	assert.False(t, found, "a harness with no parser reads none")

	// a turn that answered is read by Watch alone: a reply that talks about limits sends no one down
	l := &Limits{Now: func() time.Time { return now }}
	se := &scriptExec{outs: []string{"the card: HTTP 429: rate limit reached, please retry after 30m\n", "exit status 2: line 402\n"}, exits: []int{0, 1}}
	run := l.WatchHarness("dsh", time.Hour, se.run)
	for range 2 {
		_, _, _ = run(context.Background(), "/w", "dsh", []string{"x"}, "")
	}
	_, _, limited := l.Limited()
	assert.False(t, limited)
}

// The daemon over a DSH harness that runs out of credits: down until the
// reset (--limit-rest, no reset in the text), nothing delivered, every
// message pending, pings answered by the daemon, status session=limited,
// no beat; the wake at the reset still limited takes the next reset from
// its text; the next wake answers its nonce and the friend is up, the
// message delivered. The seat is told of each limit and of the wake.
func TestUsageLimitMarksDownUntilReset(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		clock := func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now }
		nonces := []string{"wake01", "wake02"}
		var told []string
		l := &Limits{
			Now:   clock,
			Nonce: func() string { n := nonces[0]; nonces = nonces[1:]; return n },
			Down: func(until time.Time, reason string) {
				subject, _ := LimitDownText("bob", until, reason)
				told = append(told, subject)
			},
			Up: func(string) { subject, _ := LimitUpText("bob"); told = append(told, subject) },
		}
		var mu sync.Mutex
		var calls []time.Time
		var texts []string
		outs := []struct {
			out  string
			exit int
		}{
			{"HTTP 402: insufficient balance on account. Please recharge.\n", 1}, // the message's turn: out of credits
			{"HTTP 429: rate limit reached, please retry after 1m\n", 1},         // the first wake: still limited
			{"wake02\n", 0}, // the second wake answers its nonce
			{"done\n", 0},   // the message
		}
		run := func(_ context.Context, _, _ string, _ []string, stdin string) (string, int, error) {
			mu.Lock()
			defer mu.Unlock()
			calls, texts = append(calls, clock()), append(texts, stdin)
			o := outs[len(calls)-1]
			if len(calls) == 1 {
				r.send(t, "ada", "PING n1", PingText("ada", t0, "n1")) // a ping arrives while she is down
			}
			if len(calls) == len(outs) {
				r.mu.Lock()
				r.stopAfter = r.beats + 5
				r.mu.Unlock()
			}
			return o.out, o.exit, nil
		}
		r.d.Deliver = l.Gate(&DSH{Dir: "/w/bob", Session: "s1", Run: l.WatchHarness("dsh", 2*time.Minute, run), Program: "dsh"})
		r.d.Limited = l.LimitState
		r.passive = true
		r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
		type beat struct {
			at  time.Time
			out bool // whether the beat went to the server
		}
		var beats []beat
		step := r.d.Beat
		r.d.Beat = func(ctx context.Context, active time.Time) error {
			out := false
			err := l.Beat(func(context.Context) error { out = true; return nil })(ctx)
			beats = append(beats, beat{clock(), out})
			if serr := step(ctx, active); serr != nil {
				return serr
			}
			return err
		}
		r.send(t, "ada", "hello", "x")
		r.run(t, 2000)

		require.Len(t, calls, 4, "the turn, two wakes, the message: nothing ran while limited")
		down := calls[0]
		assert.GreaterOrEqual(t, calls[1].Sub(down), 2*time.Minute, "the first wake no sooner than --limit-rest")
		assert.Less(t, calls[1].Sub(down), 2*time.Minute+2*RecheckEvery)
		assert.GreaterOrEqual(t, calls[2].Sub(calls[1]), time.Minute, "the next reset taken from the wake's text")
		assert.Contains(t, texts[1], "wake01")
		assert.Contains(t, texts[2], "wake02")
		assert.Contains(t, texts[3], "hello", "after the wake the message itself goes in")

		var during Status
		r.mu.Lock()
		for _, s := range r.status {
			if s.Session == SessionLimited {
				during = s
				break
			}
		}
		r.mu.Unlock()
		assert.Equal(t, SessionLimited, during.Session, "status.json says limited while down")
		assert.Equal(t, LimitKindCredits, during.LimitKind)
		assert.Equal(t, down.Add(2*time.Minute), during.LimitUntil)
		assert.Equal(t, SessionOK, r.last().Session, "up again")
		assert.Empty(t, r.last().LimitKind)

		pending, _ := r.pending(t)
		assert.Empty(t, pending, "the message, held through the limit, acked once delivered")
		assert.Contains(t, r.adaGot(t), "daemon-pong: daemon-pong n1", "pings answered by the daemon while limited")
		for _, line := range r.records {
			assert.NotContains(t, line, "given_up")
			assert.NotContains(t, line, "deliveries=")
		}

		require.Len(t, told, 3)
		assert.Contains(t, told[0], "friend bob down")
		assert.Contains(t, told[1], "friend bob down", "the wake still limited: down again, until the next reset")
		assert.Equal(t, "friend bob back: her harness answered the wake after its reset", told[2])

		held := 0
		for _, b := range beats {
			if b.at.After(down) && b.at.Before(calls[2]) {
				assert.False(t, b.out, "no beat while limited, at %s", b.at)
				held++
			}
		}
		assert.Greater(t, held, 10)
		assert.True(t, beats[len(beats)-1].out, "beating again once up")
	})
}
