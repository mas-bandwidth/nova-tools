package friend

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every line of testdata/limits.tsv is one text a harness prints, the kind it
// parses to and the reset it names: a text it does not recognise stays an
// ordinary failure (kind none).
func TestEachHarnessUsageLimitAndCreditsTextParsesWithItsReset(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/limits.tsv")
	require.NoError(t, err)
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	seen := map[string]map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.SplitN(line, "\t", 4)
		require.Len(t, f, 4, line)
		harness, kind, until, text := f[0], f[1], f[2], f[3]
		hit, ok := ParseLimit(harness, "working on it\n"+text+"\n", now, time.Hour)
		if kind == "none" {
			assert.False(t, ok, "%s: %s is an ordinary failure", harness, text)
			continue
		}
		if !assert.True(t, ok, "%s: %s", harness, text) {
			continue
		}
		assert.Equal(t, kind, hit.Kind, "%s: %s", harness, text)
		want := now.Add(time.Hour)
		if until != "default" {
			want, err = time.Parse(time.RFC3339, until)
			require.NoError(t, err, line)
		}
		assert.True(t, want.Equal(hit.Until), "%s: %s: until %s, want %s", harness, text, hit.Until.Format(time.RFC3339), want.Format(time.RFC3339))
		assert.Equal(t, until != "default", hit.Named, "%s: %s", harness, text)
		if seen[harness] == nil {
			seen[harness] = map[string]bool{}
		}
		seen[harness][kind] = true
	}
	for _, h := range []string{"claude", "codex", "opencode", "grok", "antigravity", "dsh", "gemini"} {
		assert.True(t, seen[h][KindLimit] && seen[h][KindCredits], "%s has a limit text and a credits text in the fixtures: %v", h, seen[h])
	}
	_, ok := ParseLimit("claude", "Credit balance is too low", now, 0)
	assert.True(t, ok, "no rest given: DefaultLimitWait")
	_, ok = ParseLimit("nobody", "Credit balance is too low", now, 0)
	assert.False(t, ok, "a harness with no words has no limit")
}

// A turn that succeeded and only talks of a limit sends no one down, and a
// failed one whose text is not a limit is an ordinary failure.
func TestAReplyThatTalksOfLimitsIsNoLimit(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		out    string
		exit   int
		limits bool
	}{
		{"The credit balance is too low in the test I wrote.\n", 0, false},
		{"Credit balance is too low\n", 1, true},
		{"cannot open file: permission denied\n", 1, false},
	} {
		downs := 0
		l := &Limits{Now: func() time.Time { return now }, Harness: "claude", Down: func(time.Time, string) { downs++ }}
		_, _, _ = l.Watch(func(context.Context, string, string, []string, string) (string, int, error) {
			return tc.out, tc.exit, nil
		})(context.Background(), "", "x", nil, "")
		_, _, limited := l.Limited()
		assert.Equal(t, tc.limits, limited, tc.out)
		if tc.limits {
			assert.Equal(t, KindCredits, l.Kind())
			assert.Equal(t, 1, downs)
		}
	}
}

// harnessExec is a fake harness: its first turn is at the limit, a wake turn
// answers its nonce, and every other turn works. It logs what it was asked.
type harnessExec struct {
	mu     sync.Mutex
	asked  []string
	limit  string
	failed bool
}

func (h *harnessExec) run(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
	text := args[len(args)-1]
	h.mu.Lock()
	defer h.mu.Unlock()
	h.asked = append(h.asked, text)
	if !h.failed {
		h.failed = true
		return "working on it\n" + h.limit + "\n", 1, nil
	}
	if _, nonce, ok := strings.Cut(text, "nothing else: "); ok {
		return strings.TrimSpace(nonce) + "\n", 0, nil
	}
	return "ran it\n", 0, nil
}

// A harness at its usage limit is down until the reset (docs/SPEC-FRIEND.md,
// limits-mean-down-w-r.w1~15): the daemon delivers nothing while limited,
// every message stays pending, a ping is answered by the daemon, the status
// says session=limited with the kind and the reset, the coordinator is told
// once; after the reset one wake turn is tried, and when it answers the
// friend is up again and the message goes in.
func TestUsageLimitMarksDownUntilReset(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		reset := t0.Add(3 * time.Minute)
		h := &harnessExec{limit: "ERROR: You've hit your usage limit. Try again at 3:03 AM."}
		clock := func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now }
		var downs, ups []string
		l := &Limits{Now: clock, Harness: "codex", Nonce: func() string { return "w4k3up" },
			Down: func(until time.Time, reason string) {
				downs = append(downs, until.UTC().Format(time.RFC3339))
				subject, body := LimitDownText("bob", until, reason)
				_, err := r.bus.Send(context.Background(), bus.Message{From: "bob", To: []string{"ada"}, Subject: subject, Body: body})
				require.NoError(t, err)
			},
			Up: func(string) {
				ups = append(ups, "up")
				subject, body := LimitUpText("bob")
				_, err := r.bus.Send(context.Background(), bus.Message{From: "bob", To: []string{"ada"}, Subject: subject, Body: body})
				require.NoError(t, err)
			},
		}
		r.d.Deliver = l.Gate(&OpenCode{Dir: "/w/bob", Session: "s1", Run: l.Watch(h.run)})
		r.d.Limited = func() (string, time.Time, bool) { until, _, limited := l.Limited(); return l.Kind(), until, limited }
		// her beat as the sprint server would take it: up, or down with the until and the
		// reason while limited (limits-mean-down-w-r5.w1~15); the rig's own beat is its step
		var beats []string
		step := r.d.Beat
		r.d.Beat = func(ctx context.Context, active time.Time) error {
			err := step(ctx, active)
			say := func(b string) { r.mu.Lock(); beats = append(beats, b); r.mu.Unlock() }
			if e := l.BeatOrDown(func(context.Context) error { say("up"); return nil }, func(_ context.Context, until time.Time, reason string) error {
				say("down until=" + until.UTC().Format(time.RFC3339) + " reason=" + reason)
				return nil
			})(ctx); e != nil {
				return e
			}
			return err
		}
		r.passive = true
		r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
		hello := r.send(t, "ada", "hello", "run the card")
		r.at[40] = func() { r.send(t, "ada", "PING p1", PingText("ada", t0, "p1")) } // while limited
		r.at[50] = func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			assert.Equal(t, 1, len(h.asked), "nothing is delivered while limited: the harness ran the one turn that hit it")
		}
		r.run(t, 400)

		require.Len(t, h.asked, 3, "the turn that hit the limit, one wake turn after the reset, then the message")
		assert.Equal(t, Text(hello), h.asked[0])
		assert.Contains(t, h.asked[1], "w4k3up", "the wake turn")
		assert.Equal(t, Text(hello), h.asked[2], "the message goes in after the wake answered, once")

		var limited []Status
		for _, s := range r.status {
			if s.Session == SessionLimited {
				limited = append(limited, s)
			}
		}
		require.NotEmpty(t, limited, "status says session=limited while it stands")
		for _, s := range limited {
			assert.Equal(t, KindLimit, s.LimitKind)
			assert.True(t, reset.Equal(s.LimitUntil), "until the reset the text named: %s", s.LimitUntil)
			assert.Equal(t, 0, s.Delivered, "counted toward nothing")
		}
		assert.Equal(t, SessionOK, r.last().Session, "up again after the wake answered")
		assert.Equal(t, "", r.last().LimitKind)
		assert.Equal(t, 1, r.last().Delivered)
		pending, fresh, err := r.bus.Peek(context.Background(), "bob")
		require.NoError(t, err)
		assert.Empty(t, pending, "acked after the wake")
		assert.Empty(t, fresh)

		r.mu.Lock()
		sent := slices.Compact(slices.Clone(beats))
		r.mu.Unlock()
		assert.Equal(t, []string{"up", "down until=2026-10-04T03:03:00Z reason=harness limit: ERROR: You've hit your usage limit. Try again at 3:03 AM.", "up"}, sent,
			"the beat says up, then down with the until and the reason while limited, then up after the wake")

		assert.Equal(t, []string{reset.UTC().Format(time.RFC3339)}, downs, "the coordinator is told once of the limit")
		assert.Equal(t, []string{"up"}, ups, "and once of the wake")
		got := strings.Join(r.adaGot(t), "\n")
		assert.Contains(t, got, "daemon-pong: daemon-pong p1", "pings are answered by the daemon while limited")
		told := map[string]int{}
		for _, m := range r.adaGot(t) {
			told[strings.SplitN(m, " ", 4)[2]]++ // "friend bob down:" or "friend bob back:", the subject's start
		}
		assert.Equal(t, 1, told["down:"], "%v", told)
		assert.Equal(t, 1, told["back:"], "%v", told)
		said := ""
		for _, line := range r.records {
			if strings.Contains(line, "session=limited kind=limit until=2026-10-04T03:03:00Z") {
				said += line
			}
		}
		assert.NotEmpty(t, said, "the record says it once")
	})
}
