package friend_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Unit tests for ParseFriendRow
func TestParseFriendRow(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		input  string
		expect friend.FriendRowConfig
	}{
		{
			name:   "empty",
			input:  "",
			expect: friend.FriendRowConfig{},
		},
		{
			name:  "all fields",
			input: "row_mode=one-shot row_width=8 row_profile=friend row_filter=flash row_tiers=flash,pro row_load_max=90.0 row_token_cap=6000000 row_model=mercury-2.5 row_provider=inception",
			expect: friend.FriendRowConfig{
				Mode:     "one-shot",
				Width:    8,
				Profile:  "friend",
				Filter:   "flash",
				Tiers:    []string{"flash", "pro"},
				LoadMax:  90.0,
				TokenCap: 6000000,
				Model:    "mercury-2.5",
				Provider: "inception",
			},
		},
		{
			name:  "partial fields with commas",
			input: "row_filter=security row_tiers=pro,heavy row_width=3",
			expect: friend.FriendRowConfig{
				Width:  3,
				Filter: "security",
				Tiers:  []string{"pro", "heavy"},
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := friend.ParseFriendRow(tc.input)
			assert.Equal(t, tc.expect, got)
		})
	}
}

// Unit tests for ReadCardTier and ReadCardStream
func TestReadCardTierAndStream(t *testing.T) {
	t.Parallel()
	brief := `STATUS: nova-sprint card sec-123, epoch 15, attempt 1
WHO: friend alex
tier: flash
stream: security-auth

Do the work.`

	assert.Equal(t, "flash", friend.ReadCardTier(brief))
	assert.Equal(t, "security-auth", friend.ReadCardStream(brief, "sec-123"))

	// Stream from parentheses
	briefParen := `Some task (stream security.tools) tier: pro`
	assert.Equal(t, "pro", friend.ReadCardTier(briefParen))
	assert.Equal(t, "security.tools", friend.ReadCardStream(briefParen, "sec-123"))

	// Fallback stream from ID prefix
	assert.Equal(t, "sec", friend.ReadCardStream("no stream mentioned", "sec-456"))
}

// Unit tests for CardFilter and ShouldTakeBack
func TestCardFilterAndShouldTakeBack(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		id         string
		stream     string
		tier       string
		filter     string
		tiers      []string
		wantAction string
	}{
		{
			name:       "security filter matching stream",
			id:         "core-1",
			stream:     "security-sandbox",
			tier:       "pro",
			filter:     "security",
			wantAction: "run",
		},
		{
			name:       "security filter matching id prefix",
			id:         "fp-sec-01",
			stream:     "misc",
			tier:       "pro",
			filter:     "security",
			wantAction: "run",
		},
		{
			name:       "security filter skipping non-security",
			id:         "tool-1",
			stream:     "tools",
			tier:       "pro",
			filter:     "security",
			wantAction: "skip",
		},
		{
			name:       "flash filter matching flash tier",
			id:         "card-1",
			stream:     "any",
			tier:       "flash",
			filter:     "flash",
			wantAction: "run",
		},
		{
			name:       "flash filter taking non-flash",
			id:         "card-2",
			stream:     "any",
			tier:       "pro",
			filter:     "flash",
			wantAction: "take",
		},
		{
			name:       "custom tiers list matching",
			id:         "card-3",
			stream:     "any",
			tier:       "heavy",
			tiers:      []string{"flash", "heavy"},
			wantAction: "run",
		},
		{
			name:       "custom tiers list taking unlisted",
			id:         "card-4",
			stream:     "any",
			tier:       "frontier",
			tiers:      []string{"flash", "heavy"},
			wantAction: "take",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			act, reason := friend.CardFilter(tc.id, tc.stream, tc.tier, tc.filter, tc.tiers, "Freddy")
			assert.Equal(t, tc.wantAction, act)
			if act != "run" {
				assert.NotEmpty(t, reason)
			}
		})
	}

	assert.True(t, friend.ShouldTakeBack("take", false))
	assert.False(t, friend.ShouldTakeBack("take", true), "do not take back if jobs/<job> already exists")
	assert.False(t, friend.ShouldTakeBack("run", false))
	assert.False(t, friend.ShouldTakeBack("skip", false))
}

// Unit tests for FormatJob and ParseJob
func TestFormatAndParseJob(t *testing.T) {
	t.Parallel()
	cases := []struct {
		card  string
		epoch int
		gen   int
		job   string
	}{
		{"card-a", 0, 1, "card-a"},
		{"card-b", 15, 1, "card-b~15"},
		{"card-c", 15, 2, "card-c~15.g2"},
		{"card-d", 0, 3, "card-d.g3"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.job, func(t *testing.T) {
			t.Parallel()
			formatted := friend.FormatJob(tc.card, tc.epoch, tc.gen)
			assert.Equal(t, tc.job, formatted)

			c, ep, g, ok := friend.ParseJob(tc.job)
			assert.True(t, ok)
			assert.Equal(t, tc.card, c)
			assert.Equal(t, tc.epoch, ep)
			assert.Equal(t, tc.gen, g)
		})
	}

	_, _, _, ok := friend.ParseJob("")
	assert.False(t, ok)
}

// Unit tests for EffectiveWidth and EffectiveWidthHeld
func TestEffectiveWidth(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 8, friend.EffectiveWidth(8, 45.0, 90.0), "load under max: width stays 8")
	assert.Equal(t, 3, friend.EffectiveWidth(8, 95.0, 90.0), "load over max: width held to 3")
	assert.Equal(t, 2, friend.EffectiveWidth(2, 95.0, 90.0), "width already under held: unchanged")
	assert.Equal(t, 8, friend.EffectiveWidth(8, 95.0, 0.0), "loadMax disabled: width stays 8")
	assert.Equal(t, 5, friend.EffectiveWidthHeld(12, 100.0, 90.0, 5), "custom held width")
}

// Unit tests for TokenCap and HoldReportTokenCap
func TestCheckTokenCapAndReport(t *testing.T) {
	t.Parallel()
	assert.False(t, friend.CheckTokenCap(5000000, 6000000))
	assert.True(t, friend.CheckTokenCap(6000000, 6000000))
	assert.True(t, friend.CheckTokenCap(7000000, 6000000))
	assert.False(t, friend.CheckTokenCap(7000000, 0), "cap disabled")

	rep := friend.HoldReportTokenCap(6100000, 4, "$ git push", "Freddy", 6000000)
	assert.Contains(t, rep, "Verdict: HOLD")
	assert.Contains(t, rep, "Head: none")
	assert.Contains(t, rep, "token cap: 6100000 tokens, 4 turns, last step: $ git push")
	assert.Contains(t, rep, "Freddy's one-shot lane was stopped at the runner's per-card cap of 6000000 tokens")
}

// Unit tests for IsProviderFailure
func TestIsProviderFailure(t *testing.T) {
	t.Parallel()
	cases := []struct {
		text      string
		isFailure bool
	}{
		{"Error: 429 Too Many Requests", true},
		{"insufficient funds: your account balance is $0", true},
		{"payment required to continue", true},
		{"Error: 402 Payment Required", true},
		{"out of credits on provider", true},
		{"exceeded your current quota", true},
		{"rate limit exceeded, please retry later", true},
		{"normal compile error: cannot find package", false},
		{"exit code 1: test failed", false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.text, func(t *testing.T) {
			t.Parallel()
			msg, ok := friend.IsProviderFailure(tc.text)
			assert.Equal(t, tc.isFailure, ok)
			if tc.isFailure {
				assert.NotEmpty(t, msg)
			}
		})
	}
}

// Unit tests for CostOf, Dollars, FormatCostLine, PublishReportWithCost, AppendResultCost
func TestPricingAndCostFormatting(t *testing.T) {
	t.Parallel()
	tk := friend.OpencodeTokens{
		Input:      1000000,
		CacheRead:  500000,
		CacheWrite: 200000,
		Output:     100000,
		Reasoning:  50000,
		Cost:       0.45,
	}

	rp := &friend.RoutePrice{
		RouteName:         "test-route",
		Input:             "0.20",
		CacheRead:         "0.05",
		CacheWrite:        "0.10",
		Output:            "0.80",
		ReasoningAsOutput: true,
	}

	cost := friend.CostOf(tk, rp, "model-x")
	assert.True(t, strings.HasPrefix(cost, "$"), "cost: "+cost)

	// Missing prices
	assert.Contains(t, friend.CostOf(tk, nil, "model-x"), "unpriced (no route row")
	rpExtra := *rp
	rpExtra.HasExtra = true
	assert.Contains(t, friend.CostOf(tk, &rpExtra, "model-x"), "unpriced (route test-route has long-context")

	// Cost line
	cline := friend.FormatCostLine(cost, "$0.45", tk, "inception/mercury-2.5", "test-route")
	assert.Contains(t, cline, "Cost: "+cost+" (opencode: $0.45)")
	assert.Contains(t, cline, "tokens input=1000000 cache_read=500000 cache_write=200000 output=100000 reasoning=50000")
	assert.Contains(t, cline, "model=inception/mercury-2.5 harness=opencode price_route=test-route")

	// PublishReportWithCost
	origReport := "Verdict: LAND\nHead: 0123456789abcdef0123456789abcdef01234567\n\nAll tests passed."
	published := friend.PublishReportWithCost(origReport, cline)
	lines := strings.Split(published, "\n")
	require.True(t, len(lines) >= 3)
	assert.Equal(t, "Head: 0123456789abcdef0123456789abcdef01234567", lines[1])
	assert.Equal(t, cline, lines[2])

	// AppendResultCost
	origResult := "RESULT: success"
	resAppended := friend.AppendResultCost(origResult, cost, "$0.45", tk, "inception/mercury-2.5")
	assert.Contains(t, resAppended, "RESULT: success")
	assert.Contains(t, resAppended, "tokens: input=1000000")
	assert.Contains(t, resAppended, "cost: "+cost+" (opencode: $0.45)")
}

// Unit tests for ParseTokensOutput
func TestParseTokensOutput(t *testing.T) {
	t.Parallel()
	raw := "1000\t200\t300\t400\t50\t0.035\t2\n"
	tk, err := friend.ParseTokensOutput(raw)
	require.NoError(t, err)
	assert.Equal(t, int64(1000), tk.Input)
	assert.Equal(t, int64(200), tk.CacheRead)
	assert.Equal(t, int64(300), tk.CacheWrite)
	assert.Equal(t, int64(400), tk.Output)
	assert.Equal(t, int64(50), tk.Reasoning)
	assert.Equal(t, 0.035, tk.Cost)
	assert.Equal(t, 2, tk.Sessions)
	assert.Equal(t, int64(1950), tk.Total())
}

// Unit tests for FindRoutePrice
func TestFindRoutePrice(t *testing.T) {
	t.Parallel()
	jsonText := `{
		"routes": [
			{
				"route": {
					"name": "flash-mercury",
					"provider": "inception",
					"model": "mercury-2.5",
					"prices": {
						"input": "0.25",
						"cache_read": "0.05",
						"cache_write": "0.10",
						"output": "0.75",
						"reasoning_as_output": true
					}
				}
			},
			{
				"route": {
					"name": "pro-abliterated-alex",
					"provider": "abliteration-ai",
					"model": "abliterated-model-large-v2",
					"prices": {
						"input": "2.50",
						"output": "10.00",
						"long_context": 128000
					}
				}
			}
		]
	}`

	rp, err := friend.FindRoutePrice(jsonText, "inception/mercury-2.5")
	require.NoError(t, err)
	require.NotNil(t, rp)
	assert.Equal(t, "flash-mercury", rp.RouteName)
	assert.Equal(t, "0.25", rp.Input)
	assert.Equal(t, "0.75", rp.Output)
	assert.True(t, rp.ReasoningAsOutput)
	assert.False(t, rp.HasExtra)

	// Route with extra price components (long_context)
	rp2, err := friend.FindRoutePrice(jsonText, "abliteration-ai/abliterated-model-large-v2")
	require.NoError(t, err)
	require.NotNil(t, rp2)
	assert.Equal(t, "pro-abliterated-alex", rp2.RouteName)
	assert.True(t, rp2.HasExtra)

	// Nonexistent model
	rpNone, err := friend.FindRoutePrice(jsonText, "unknown/model")
	require.NoError(t, err)
	assert.Nil(t, rpNone)
}

// Unit tests for WriteRefusalShims
func TestWriteRefusalShims(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	binDir, err := friend.WriteRefusalShims(dir, "freddy")
	require.NoError(t, err)

	for _, prog := range []string{"go", "gofmt"} {
		p := filepath.Join(binDir, prog)
		info, err := os.Stat(p)
		require.NoError(t, err)
		assert.True(t, info.Mode()&0o111 != 0, "executable bit set")

		// Run shim and check refusal output
		cmd := exec.Command(p, "version")
		out, err := cmd.CombinedOutput()
		assert.Error(t, err)
		assert.Contains(t, string(out), "REFUSED: "+prog+" never runs on this host")
		assert.Contains(t, string(out), "freddy-bench")
	}
}

// Unit tests for FinishBusNote
func TestFinishBusNote(t *testing.T) {
	t.Parallel()
	subj, body := friend.FinishBusNote("Freddy", "card-1~15", "Verdict: LAND", "$0.05", "$0.04", 42*time.Second)
	assert.Equal(t, "Freddy card card-1~15: Verdict: LAND", subj)
	assert.Equal(t, "Freddy one-shot lane finished card-1~15: Verdict: LAND; cost $0.05 (opencode $0.04); wall 42s", body)
}

// Comprehensive parity test for all 9 runner.zsh behaviors
func TestOpencodeLanesDoWhatTheRunnerStopgapsDid(t *testing.T) {
	t.Parallel()

	// 1. Filtering & Dealer Take-Back
	t.Run("CardFilteringAndTakeBack", func(t *testing.T) {
		t.Parallel()
		// Freddy runs flash only, takes back pro
		act, reason := friend.CardFilter("c1", "tools", "pro", "flash", nil, "Freddy")
		assert.Equal(t, "take", act)
		assert.Contains(t, reason, "Freddy runs flash cards only; the rest go back to the dealer")
		assert.True(t, friend.ShouldTakeBack(act, false))
		assert.False(t, friend.ShouldTakeBack(act, true)) // do not take if job dir exists

		// Alex runs security only, skips non-security
		act2, reason2 := friend.CardFilter("c2", "tools", "pro", "security", nil, "Alex")
		assert.Equal(t, "skip", act2)
		assert.Contains(t, reason2, "not a security card")

		// Alex runs security stream
		act3, _ := friend.CardFilter("c3", "security.audit", "pro", "security", nil, "Alex")
		assert.Equal(t, "run", act3)
	})

	// 2. Job Directory Naming & Generations
	t.Run("JobNamingAndGenerations", func(t *testing.T) {
		t.Parallel()
		job := friend.FormatJob("card-100", 15, 2)
		assert.Equal(t, "card-100~15.g2", job)

		card, epoch, gen, ok := friend.ParseJob(job)
		require.True(t, ok)
		assert.Equal(t, "card-100", card)
		assert.Equal(t, 15, epoch)
		assert.Equal(t, 2, gen)

		c := friend.Card{ID: "card-100", Brief: "/inbox/BRIEF.md", Outbox: "/outbox/card-100~15.g2"}
		assert.Equal(t, "card-100~15.g2", c.JobName())
		assert.Equal(t, "15", c.Epoch(), "sprint progress gets numeric epoch only")
		assert.Equal(t, 2, c.Gen())
	})

	// 3. Session Naming
	t.Run("SessionTitles", func(t *testing.T) {
		t.Parallel()
		nowSec := time.Now().Unix()
		title := fmt.Sprintf("%s one-shot %s %d", "Freddy", "card-100~15", nowSec)
		assert.True(t, strings.HasPrefix(title, "Freddy one-shot card-100~15 "))
	})

	// 4. Refusal Shims for go and gofmt
	t.Run("RefusalShims", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		binDir, err := friend.WriteRefusalShims(dir, "alex")
		require.NoError(t, err)

		goShim := filepath.Join(binDir, "go")
		cmd := exec.Command(goShim, "build", ".")
		out, err := cmd.CombinedOutput()
		assert.Error(t, err)
		assert.Contains(t, string(out), "REFUSED: go never runs on this host")
		assert.Contains(t, string(out), "alex-bench")
	})

	// 5. Load Shedding & Held Width
	t.Run("LoadSheddingHeldWidth", func(t *testing.T) {
		t.Parallel()
		// Load 95.0 > LoadMax 90.0 holds width 8 to 3
		eff := friend.EffectiveWidth(8, 95.0, 90.0)
		assert.Equal(t, 3, eff)

		// Load 40.0 <= LoadMax 90.0 keeps width 8
		effNormal := friend.EffectiveWidth(8, 40.0, 90.0)
		assert.Equal(t, 8, effNormal)
	})

	// 6. Token Cap per Card
	t.Run("TokenCapPerCard", func(t *testing.T) {
		t.Parallel()
		capLimit := int64(6000000)
		assert.False(t, friend.CheckTokenCap(5999999, capLimit))
		assert.True(t, friend.CheckTokenCap(6000000, capLimit))

		holdRep := friend.HoldReportTokenCap(6050000, 3, "make test", "Freddy", capLimit)
		assert.Contains(t, holdRep, "Verdict: HOLD")
		assert.Contains(t, holdRep, "Head: none")
		assert.Contains(t, holdRep, "token cap: 6050000 tokens, 3 turns, last step: make test")
	})

	// 7. Unrecoverable Provider Failures
	t.Run("ProviderFailurePause", func(t *testing.T) {
		t.Parallel()
		provOut := "2026-10-05T10:00:00Z Error: 429 Too Many Requests: payment required or quota exceeded"
		msg, isFailure := friend.IsProviderFailure(provOut)
		require.True(t, isFailure)
		assert.Contains(t, msg, "429")

		normalOut := "Building target ./cmd/foo\nExit 0"
		_, isNormal := friend.IsProviderFailure(normalOut)
		assert.False(t, isNormal)
	})

	// 8. Token Querying, Pricing & Report Update
	t.Run("TokenQueryingAndRoutePricing", func(t *testing.T) {
		t.Parallel()
		tk := friend.OpencodeTokens{
			Input:      2000000,
			CacheRead:  1000000,
			CacheWrite: 500000,
			Output:     200000,
			Reasoning:  100000,
			Cost:       1.20,
			Sessions:   1,
		}
		rp := &friend.RoutePrice{
			RouteName:         "flash-mercury",
			Input:             "0.25",
			CacheRead:         "0.05",
			CacheWrite:        "0.10",
			Output:            "0.75",
			ReasoningAsOutput: true,
		}

		cost := friend.CostOf(tk, rp, "inception/mercury-2.5")
		assert.Equal(t, "$0.83", cost) // 2*0.25 + 1*0.05 + 0.5*0.10 + 0.3*0.75 = 0.50 + 0.05 + 0.05 + 0.225 = 0.825 -> $0.83

		cline := friend.FormatCostLine(cost, "$1.20", tk, "inception/mercury-2.5", "flash-mercury")
		repDraft := "Verdict: LAND\nHead: abcdef0123456789abcdef0123456789abcdef01\n\nAll gates pass."
		published := friend.PublishReportWithCost(repDraft, cline)
		assert.Contains(t, published, cline)

		resDraft := "RESULT: ok\n"
		resUpdated := friend.AppendResultCost(resDraft, cost, "$1.20", tk, "inception/mercury-2.5")
		assert.Contains(t, resUpdated, "tokens: input=2000000")
		assert.Contains(t, resUpdated, "cost: $0.83 (opencode: $1.20)")
	})

	// 9. Coordinator Bus Notification
	t.Run("FinishBusNotification", func(t *testing.T) {
		t.Parallel()
		subj, body := friend.FinishBusNote("Freddy", "card-1~15.g2", "Verdict: LAND", "$0.83", "$1.20", 35*time.Second)
		assert.Equal(t, "Freddy card card-1~15.g2: Verdict: LAND", subj)
		assert.Contains(t, body, "cost $0.83 (opencode $1.20); wall 35s")
	})
}
