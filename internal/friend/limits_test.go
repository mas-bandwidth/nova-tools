package friend

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsageLimitParsers(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	defaultRest := time.Hour

	cases := []struct {
		name       string
		harness    string
		output     string
		wantOK     bool
		wantKind   string
		wantUntil  time.Time
		wantReason string
	}{
		// Claude
		{
			name:       "claude rate limit with relative hours",
			harness:    "claude",
			output:     `{"type": "error", "error": {"type": "rate_limit_error", "message": "Number of request tokens has exceeded your daily rate limit of 100000. Try again in 2 hours."}}`,
			wantOK:     true,
			wantKind:   LimitKindLimit,
			wantUntil:  now.Add(2 * time.Hour),
			wantReason: `{"type": "error", "error": {"type": "rate_limit_error", "message": "Number of request tokens has exceeded your daily rate limit of 100000. Try again in 2 hours."}}`,
		},
		{
			name:       "claude credits exhausted",
			harness:    "claude",
			output:     `{"type": "error", "error": {"type": "invalid_request_error", "message": "Your credit balance is too low to access the Anthropic API. Please go to Plans & Billing to upgrade or purchase credits."}}`,
			wantOK:     true,
			wantKind:   LimitKindCredits,
			wantUntil:  now.Add(defaultRest),
			wantReason: `{"type": "error", "error": {"type": "invalid_request_error", "message": "Your credit balance is too low to access the Anthropic API. Please go to Plans & Billing to upgrade or purchase credits."}}`,
		},

		// Codex
		{
			name:       "codex rate limit exceeded with relative minutes",
			harness:    "codex",
			output:     `{"error": {"message": "Rate limit reached for model gpt-4 in organization org-123. Please try again in 15m.", "type": "rate_limit_exceeded", "code": 429}}`,
			wantOK:     true,
			wantKind:   LimitKindLimit,
			wantUntil:  now.Add(15 * time.Minute),
			wantReason: `{"error": {"message": "Rate limit reached for model gpt-4 in organization org-123. Please try again in 15m.", "type": "rate_limit_exceeded", "code": 429}}`,
		},
		{
			name:       "codex insufficient quota",
			harness:    "codex",
			output:     `{"error": {"message": "You exceeded your current quota, please check your plan and billing details.", "type": "insufficient_quota", "code": 402}}`,
			wantOK:     true,
			wantKind:   LimitKindCredits,
			wantUntil:  now.Add(defaultRest),
			wantReason: `{"error": {"message": "You exceeded your current quota, please check your plan and billing details.", "type": "insufficient_quota", "code": 402}}`,
		},

		// OpenCode
		{
			name:       "opencode rate limit with relative minutes",
			harness:    "opencode",
			output:     `Error 429: rate limit exceeded. Please wait 10m before submitting more requests.`,
			wantOK:     true,
			wantKind:   LimitKindLimit,
			wantUntil:  now.Add(10 * time.Minute),
			wantReason: `Error 429: rate limit exceeded. Please wait 10m before submitting more requests.`,
		},
		{
			name:       "opencode credits refresh time",
			harness:    "opencode",
			output:     `Insufficient AI Credits. Your credits will refresh 6:52 PM.`,
			wantOK:     true,
			wantKind:   LimitKindCredits,
			wantUntil:  time.Date(2026, 10, 4, 18, 52, 0, 0, time.UTC),
			wantReason: `Insufficient AI Credits. Your credits will refresh 6:52 PM.`,
		},

		// Grok
		{
			name:       "grok rate limit 429 with 24h clock time",
			harness:    "grok",
			output:     `{"code": 429, "error": "Rate limit reached. Try again at 18:30:00"}`,
			wantOK:     true,
			wantKind:   LimitKindLimit,
			wantUntil:  time.Date(2026, 10, 4, 18, 30, 0, 0, time.UTC),
			wantReason: `{"code": 429, "error": "Rate limit reached. Try again at 18:30:00"}`,
		},
		{
			name:       "grok credits exhausted",
			harness:    "grok",
			output:     `{"code": 402, "error": "AI credits exhausted for this billing cycle"}`,
			wantOK:     true,
			wantKind:   LimitKindCredits,
			wantUntil:  now.Add(defaultRest),
			wantReason: `{"code": 402, "error": "AI credits exhausted for this billing cycle"}`,
		},

		// Antigravity
		{
			name:       "antigravity resource exhausted with RFC3339",
			harness:    "antigravity",
			output:     `429 RESOURCE_EXHAUSTED: Quota exceeded for quota metric 'GenerateContent requests' and limit 'GenerateContent requests per minute'. Reset at 2026-10-04T15:00:00Z`,
			wantOK:     true,
			wantKind:   LimitKindLimit,
			wantUntil:  time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC),
			wantReason: `429 RESOURCE_EXHAUSTED: Quota exceeded for quota metric 'GenerateContent requests' and limit 'GenerateContent requests per minute'. Reset at 2026-10-04T15:00:00Z`,
		},
		{
			name:       "antigravity billing out of credits",
			harness:    "antigravity",
			output:     `402 Payment Required: Your Google Cloud billing account has run out of credits.`,
			wantOK:     true,
			wantKind:   LimitKindCredits,
			wantUntil:  now.Add(defaultRest),
			wantReason: `402 Payment Required: Your Google Cloud billing account has run out of credits.`,
		},

		// DSH
		{
			name:       "dsh rate limit 429",
			harness:    "dsh",
			output:     `HTTP 429: rate limit reached, please retry after 30m`,
			wantOK:     true,
			wantKind:   LimitKindLimit,
			wantUntil:  now.Add(30 * time.Minute),
			wantReason: `HTTP 429: rate limit reached, please retry after 30m`,
		},
		{
			name:       "dsh 402 insufficient balance",
			harness:    "dsh",
			output:     `HTTP 402: insufficient balance on account. Please recharge.`,
			wantOK:     true,
			wantKind:   LimitKindCredits,
			wantUntil:  now.Add(defaultRest),
			wantReason: `HTTP 402: insufficient balance on account. Please recharge.`,
		},

		// Gemini
		{
			name:       "gemini resource exhausted 429",
			harness:    "gemini",
			output:     `google.api_core.exceptions.ResourceExhausted: 429 Quota exceeded for gemini-2.5-pro`,
			wantOK:     true,
			wantKind:   LimitKindLimit,
			wantUntil:  now.Add(defaultRest),
			wantReason: `google.api_core.exceptions.ResourceExhausted: 429 Quota exceeded for gemini-2.5-pro`,
		},
		{
			name:       "gemini prepaid credits exhausted 402",
			harness:    "gemini",
			output:     `google.api_core.exceptions.GoogleAPICallError: 402 Prepaid credits exhausted`,
			wantOK:     true,
			wantKind:   LimitKindCredits,
			wantUntil:  now.Add(defaultRest),
			wantReason: `google.api_core.exceptions.GoogleAPICallError: 402 Prepaid credits exhausted`,
		},

		// Ordinary failure / unrecognized
		{
			name:    "unrecognized compilation error stays ordinary failure",
			harness: "claude",
			output:  `exit status 2: undefined: SomeSymbol`,
			wantOK:  false,
		},
		{
			name:    "generic internal server error 500 stays ordinary failure",
			harness: "codex",
			output:  `500 Internal Server Error`,
			wantOK:  false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			info, ok := ParseUsageLimit(tc.harness, tc.output, now, defaultRest)
			if !tc.wantOK {
				assert.False(t, ok, "expected no usage limit match")
				return
			}
			require.True(t, ok, "expected usage limit match")
			assert.Equal(t, tc.wantKind, info.Kind)
			assert.Equal(t, tc.harness, info.Harness)
			assert.Equal(t, tc.wantUntil.UTC(), info.Until.UTC())
			assert.Equal(t, tc.wantReason, info.Reason)
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
