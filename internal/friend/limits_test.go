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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readFixtureLines(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	var lines []string
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func TestUsageLimitParsers(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	defaultRest := time.Hour

	claudeLines := readFixtureLines(t, "claude.txt")
	require.Len(t, claudeLines, 2)

	codexLines := readFixtureLines(t, "codex.txt")
	require.Len(t, codexLines, 2)

	opencodeLines := readFixtureLines(t, "opencode.txt")
	require.Len(t, opencodeLines, 2)

	grokLines := readFixtureLines(t, "grok.txt")
	require.Len(t, grokLines, 2)

	antigravityLines := readFixtureLines(t, "antigravity.txt")
	require.Len(t, antigravityLines, 2)

	dshLines := readFixtureLines(t, "dsh.txt")
	require.Len(t, dshLines, 2)

	geminiLines := readFixtureLines(t, "gemini.txt")
	require.Len(t, geminiLines, 2)

	cases := []struct {
		name       string
		harness    string
		output     string
		wantKind   string
		wantUntil  time.Time
		wantReason string
	}{
		// Claude
		{
			name:       "claude rate limit with relative hours",
			harness:    "claude",
			output:     claudeLines[0],
			wantKind:   LimitKindLimit,
			wantUntil:  now.Add(2 * time.Hour),
			wantReason: claudeLines[0],
		},
		{
			name:       "claude credits exhausted",
			harness:    "claude",
			output:     claudeLines[1],
			wantKind:   LimitKindCredits,
			wantUntil:  now.Add(defaultRest),
			wantReason: claudeLines[1],
		},

		// Codex
		{
			name:       "codex rate limit exceeded with relative minutes",
			harness:    "codex",
			output:     codexLines[0],
			wantKind:   LimitKindLimit,
			wantUntil:  now.Add(15 * time.Minute),
			wantReason: codexLines[0],
		},
		{
			name:       "codex insufficient quota",
			harness:    "codex",
			output:     codexLines[1],
			wantKind:   LimitKindCredits,
			wantUntil:  now.Add(defaultRest),
			wantReason: codexLines[1],
		},

		// OpenCode
		{
			name:       "opencode rate limit with relative minutes",
			harness:    "opencode",
			output:     opencodeLines[0],
			wantKind:   LimitKindLimit,
			wantUntil:  now.Add(10 * time.Minute),
			wantReason: opencodeLines[0],
		},
		{
			name:       "opencode credits refresh time",
			harness:    "opencode",
			output:     opencodeLines[1],
			wantKind:   LimitKindCredits,
			wantUntil:  time.Date(2026, 10, 4, 18, 52, 0, 0, time.UTC),
			wantReason: opencodeLines[1],
		},

		// Grok
		{
			name:       "grok rate limit 429 with 24h clock time",
			harness:    "grok",
			output:     grokLines[0],
			wantKind:   LimitKindLimit,
			wantUntil:  time.Date(2026, 10, 4, 18, 30, 0, 0, time.UTC),
			wantReason: grokLines[0],
		},
		{
			name:       "grok credits exhausted",
			harness:    "grok",
			output:     grokLines[1],
			wantKind:   LimitKindCredits,
			wantUntil:  now.Add(defaultRest),
			wantReason: grokLines[1],
		},

		// Antigravity
		{
			name:       "antigravity resource exhausted with RFC3339",
			harness:    "antigravity",
			output:     antigravityLines[0],
			wantKind:   LimitKindLimit,
			wantUntil:  time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC),
			wantReason: antigravityLines[0],
		},
		{
			name:       "antigravity billing out of credits",
			harness:    "antigravity",
			output:     antigravityLines[1],
			wantKind:   LimitKindCredits,
			wantUntil:  now.Add(defaultRest),
			wantReason: antigravityLines[1],
		},

		// DSH
		{
			name:       "dsh rate limit 429",
			harness:    "dsh",
			output:     dshLines[0],
			wantKind:   LimitKindLimit,
			wantUntil:  now.Add(30 * time.Minute),
			wantReason: dshLines[0],
		},
		{
			name:       "dsh 402 insufficient balance",
			harness:    "dsh",
			output:     dshLines[1],
			wantKind:   LimitKindCredits,
			wantUntil:  now.Add(defaultRest),
			wantReason: dshLines[1],
		},

		// Gemini
		{
			name:       "gemini resource exhausted 429",
			harness:    "gemini",
			output:     geminiLines[0],
			wantKind:   LimitKindLimit,
			wantUntil:  now.Add(defaultRest),
			wantReason: geminiLines[0],
		},
		{
			name:       "gemini prepaid credits exhausted 402",
			harness:    "gemini",
			output:     geminiLines[1],
			wantKind:   LimitKindCredits,
			wantUntil:  now.Add(defaultRest),
			wantReason: geminiLines[1],
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			info, ok := ParseUsageLimit(tc.harness, tc.output, now, defaultRest)
			require.True(t, ok, "expected usage limit match for %s: %s", tc.harness, tc.output)
			assert.Equal(t, tc.wantKind, info.Kind)
			assert.Equal(t, tc.harness, info.Harness)
			assert.Equal(t, tc.wantUntil.UTC(), info.Until.UTC())
			assert.Equal(t, tc.wantReason, info.Reason)
		})
	}

	// Unrecognized / ordinary failure lines from testdata/unrecognized.txt
	unrecLines := readFixtureLines(t, "unrecognized.txt")
	require.NotEmpty(t, unrecLines)
	for _, text := range unrecLines {
		text := text
		t.Run("unrecognized/"+text, func(t *testing.T) {
			t.Parallel()
			for h := range Parsers {
				_, ok := ParseUsageLimit(h, text, now, defaultRest)
				assert.False(t, ok, "expected unrecognized failure for harness %s: %s", h, text)
			}
		})
	}
}

func TestUsageLimitMarksDownUntilReset(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		r.passive = true
		r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
		r.d.Coordinator = "ada"

		var mu sync.Mutex
		var beatDownCalls []struct {
			reason string
			until  time.Time
		}
		var beatUpCalls int
		r.d.BeatDown = func(ctx context.Context, reason string, until time.Time) error {
			mu.Lock()
			defer mu.Unlock()
			beatDownCalls = append(beatDownCalls, struct {
				reason string
				until  time.Time
			}{reason, until})
			return nil
		}
		r.d.BeatUp = func(ctx context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			beatUpCalls++
			return nil
		}

		deliveries := 0
		reset1 := t0.Add(6 * BeatEvery)  // reset at step 6
		reset2 := t0.Add(12 * BeatEvery) // reset at step 12

		r.d.Deliver = deliverFunc(func(ctx context.Context, text string) (int, error) {
			mu.Lock()
			defer mu.Unlock()
			deliveries++
			switch deliveries {
			case 1:
				return 1, UsageLimit{
					Harness: "claude",
					Kind:    LimitKindLimit,
					Until:   reset1,
					Reason:  "claude: 429 rate_limit_error: Rate limit reached.",
				}
			case 2:
				// First reset attempt also hit limit (returns next reset)
				return 1, UsageLimit{
					Harness: "claude",
					Kind:    LimitKindLimit,
					Until:   reset2,
					Reason:  "claude: 429 rate_limit_error: Still rate limited.",
				}
			default:
				// Second reset attempt answers!
				return 0, nil
			}
		})

		// Send initial ping and first message
		r.send(t, "ada", "PING n1", PingText("ada", t0, "n1"))
		r.send(t, "ada", "first", "hello")

		// Step 3: while limited, send a message and another ping
		r.at[3] = func() {
			r.send(t, "ada", "second", "world")
			r.send(t, "ada", "PING n2", PingText("ada", t0, "n2"))
		}

		// Step 5: advance claim so the first message can be retried at reset1 (step 6)
		r.at[5] = func() {
			r.store.Advance(bus.ClaimAfter)
		}

		// Step 11: advance claim again so the message can be retried at reset2 (step 12)
		r.at[11] = func() {
			r.store.Advance(bus.ClaimAfter)
		}

		r.run(t, 20)

		mu.Lock()
		defer mu.Unlock()

		// 3 Deliver calls: 1 initial, 1 at reset1 (still limited), 1 at reset2 (succeeded)
		assert.Equal(t, 3, deliveries)
		require.Len(t, beatDownCalls, 2)
		assert.Equal(t, reset1, beatDownCalls[0].until)
		assert.Equal(t, reset2, beatDownCalls[1].until)
		assert.Equal(t, 1, beatUpCalls)

		s := r.last()
		assert.Equal(t, SessionOK, s.Session)
		assert.Equal(t, "", s.Kind)
		assert.True(t, s.Until.IsZero())
		assert.Equal(t, 2, s.Delivered) // both first and second were delivered and acked

		// Check what coordinator ada received:
		// 1. daemon-pong n1
		// 2. told limited (down until reset1)
		// 3. daemon-pong n2 (answered while limited!)
		// 4. told limited again (down until reset2)
		// 5. told up again after limit reset
		got := r.adaGot(t)
		require.Len(t, got, 5)
		assert.Contains(t, got[0], "daemon-pong: daemon-pong n1")
		assert.Contains(t, got[1], "friend bob: limit limit: down until")
		assert.Contains(t, got[2], "daemon-pong: daemon-pong n2")
		assert.Contains(t, got[3], "friend bob: limit limit: down until")
		assert.Contains(t, got[4], "friend bob: up again after limit reset")

		// Pending messages on bob are now empty (both acked)
		pending, fresh := r.pending(t)
		assert.Empty(t, pending)
		assert.Empty(t, fresh)
	})
}
