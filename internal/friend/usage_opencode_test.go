package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A finish's usage is read from opencode's own session record, never the model's
// report (docs/SPEC-FRIEND.md, usage capture per adapter): the tokens summed over
// the record's steps, and the provider and model the steps ran.

// A session's record sums to the finish's usage: its assistant steps' tokens by
// class, and the model from the same record. The cap reads the same parse, so the
// count and the finish cannot drift.
func TestSessionUsageOfSumsTheSessionsTokensAndModel(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("testdata", "opencode-session.json"))
	require.NoError(t, err)
	u, model, err := SessionUsageOf(string(raw))
	require.NoError(t, err)
	assert.Equal(t, cardcost.Tokens{Input: 150, CacheRead: 30, CacheWrite: 40, Output: 30, Reasoning: 5, Requests: cardcost.Unreported, MaxPrompt: cardcost.Unreported}, u.Tokens)
	assert.Equal(t, "inception/mercury-2.5", model)

	// the cap reads the same parse's tokens alone
	toks, err := SessionTokens(string(raw))
	require.NoError(t, err)
	assert.Equal(t, Tokens{Input: 150, CacheRead: 30, CacheWrite: 40, Output: 30, Reasoning: 5}, toks)

	// a record with no assistant step at all is a session just opened: its baseline
	// is zero, never an unknown; one with assistant steps but no tokens is a usage
	// that cannot be read
	_, _, err = SessionUsageOf(`{"messages":[{"info":{"role":"user"}}]}`)
	assert.ErrorIs(t, err, errEmptySession)
	_, _, err = SessionUsageOf(`{"messages":[{"info":{"role":"assistant"}}]}`)
	assert.True(t, errors.Is(err, errNoTokenShape))
	_, _, err = SessionUsageOf(`not json`)
	assert.Error(t, err)
}

// A record whose modelID carries no provider keeps the provider from providerID, so the
// finish's model is "provider/model" whatever the record's fields say (the provider and
// model come from the same record).
func TestSessionUsageOfKeepsTheProviderWithTheModel(t *testing.T) {
	t.Parallel()
	_, model, err := SessionUsageOf(`{"messages":[{"info":{"role":"assistant","modelID":"mercury-2.5","providerID":"inception","tokens":{"input":1,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}}}]}`)
	require.NoError(t, err)
	assert.Equal(t, "inception/mercury-2.5", model)

	// a modelID that already carries the provider is left as it is
	_, model, err = SessionUsageOf(`{"messages":[{"info":{"role":"assistant","modelID":"inception/mercury-2.5","providerID":"inception","tokens":{"input":1,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}}}]}`)
	require.NoError(t, err)
	assert.Equal(t, "inception/mercury-2.5", model)
}

// A lane whose session was empty when its card began has a zero baseline: the finish
// carries exactly the tokens the record reports, never one more per class. The old
// baseline was cardcost.None() (-1), so the subtraction's floor returned cur+1 for
// every class and an all-zero record priced as {1,1,1,1,1} instead of being refused
// (CHANGE 1/3).
func TestAnEmptyStartingSessionHasAZeroBaseline(t *testing.T) {
	t.Parallel()
	cur, model, err := SessionUsageOf(`{"messages":[{"info":{"role":"assistant","modelID":"mercury-2.5","providerID":"inception","tokens":{"input":100,"output":20,"reasoning":0,"cache":{"read":0,"write":0}}}}]}`)
	require.NoError(t, err)
	assert.Equal(t, "inception/mercury-2.5", model)

	// the baseline a lane sets when its session's record has no assistant step at all
	spent := cur.Sub(LaneTokens{})
	assert.Equal(t, cardcost.Tokens{Input: 100, Output: 20, CacheRead: 0, CacheWrite: 0, Reasoning: 0, Requests: cardcost.Unreported, MaxPrompt: cardcost.Unreported}, spent.Tokens)

	// an all-zero record under that baseline is still zero, so the ledger refuses it
	zero, _, err := SessionUsageOf(`{"messages":[{"info":{"role":"assistant","tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}}}]}`)
	require.NoError(t, err)
	spent = zero.Sub(LaneTokens{})
	assert.Zero(t, spent.Tokens.Total(), "an empty session's zero record stays zero")
	rp := RoutePrice{Name: "flash-mercury", Found: true, Prices: cardcost.Prices{Input: "0", Output: "0", ReasoningAsOutput: true}}
	assert.Contains(t, FinishCost(spent.Tokens, rp, "inception/mercury-2.5"), "unpriced")
}

// A session record the adapter cannot read is finished usage=unknown, and the seat
// is told once with the card and why, never priced as free.
func TestAMissingSessionRecordIsUsageUnknownWithTheJudgment(t *testing.T) {
	t.Parallel()
	p := &OpenCodePriced{OpenCode: &OpenCode{Dir: t.TempDir(), Run: func(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
		assert.Equal(t, []string{"export", "ses_missing"}, args)
		return "session not found", 1, nil
	}}}
	_, _, err := p.SessionUsage("ses_missing")
	require.Error(t, err)
	subject, body := UsageUnknownNote("bob", "c1", err.Error())
	assert.Equal(t, "friend bob: usage unknown for card c1", subject)
	assert.Contains(t, body, "usage unknown for card c1")
	assert.Contains(t, body, "ses_missing")
	assert.Contains(t, body, "never $0.00")
	assert.NotContains(t, subject, "$")

	// no export runner is an unknown usage too
	_, _, err = (&OpenCodePriced{OpenCode: &OpenCode{Dir: t.TempDir()}}).SessionUsage("ses_1")
	require.Error(t, err)
}

// A route's intro-rate price applies only to tokens that were counted: a route found
// with zero tokens is unpriced, never $0.00.
func TestAnIntroRateRouteWithZeroTokensIsUnpriced(t *testing.T) {
	t.Parallel()
	rp := RoutePrice{Name: "flash-mercury", Found: true, Prices: cardcost.Prices{Input: "0", Output: "0", ReasoningAsOutput: true}}
	zero := cardcost.Tokens{Input: 0, Output: 0, Requests: cardcost.Unreported, MaxPrompt: cardcost.Unreported}
	assert.Equal(t, "unpriced (zero tokens: the intro-rate price is applied only to counted tokens)", FinishCost(zero, rp, "inception/mercury-2.5"))

	// the same rule in the cost line the finish publishes: no dollar figure
	report, tokens, cost := CostLineOf(LaneTokens{Tokens: zero}, rp, "inception/mercury-2.5", "")
	assert.Equal(t, "cost: unpriced (zero tokens: the intro-rate price is applied only to counted tokens)", cost)
	assert.NotContains(t, report, "$0.00")
	assert.Contains(t, tokens, "model=inception/mercury-2.5")
	assert.Contains(t, tokens, "input=unreported")

	// a usage that could not be read says so, never a dollar figure, and its
	// tokens read unreported, never as zeros
	_, tokens, cost = CostLineOf(LaneTokens{Tokens: cardcost.None()}, rp, "inception/mercury-2.5", "opencode session record ses_x: not found")
	assert.Contains(t, cost, "unpriced (usage unknown:")
	assert.NotContains(t, cost, "$")
	assert.Contains(t, tokens, "input=unreported")

	// a route with real tokens is priced as before
	assert.Equal(t, "$0.25", FinishCost(cardcost.Tokens{Input: 1_000_000, Requests: cardcost.Unreported, MaxPrompt: cardcost.Unreported}, RoutePrice{Name: "flash-mercury", Found: true, Prices: cardcost.Prices{Input: "0.25"}}, "inception/mercury-2.5"))
}

func TestSessionUsageReadsExportAndReplacesModelAccounting(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("testdata", "opencode-session.json"))
	require.NoError(t, err)
	p := &OpenCodePriced{OpenCode: &OpenCode{Dir: t.TempDir(), Run: func(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
		assert.Equal(t, []string{"export", "ses_abc123"}, args)
		return "Exporting session\n" + string(raw), 0, nil
	}}}
	u, model, err := p.SessionUsage("ses_abc123")
	require.NoError(t, err)
	assert.Equal(t, int64(150), u.Tokens.Input)
	assert.Equal(t, "inception/mercury-2.5", model)

	outbox := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outbox, "REPORT.md"), []byte("Verdict: LAND\nHead: abc\nCost: $0.00 tokens input=0\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outbox, "RESULT.md"), []byte("head: abc\ntokens: input=0\ncost: $0.00\n"), 0o644))
	rp := RoutePrice{Name: "flash-mercury", Found: true, Prices: cardcost.Prices{Input: "0.25"}}
	require.NoError(t, PublishFinishCost(outbox, u, rp, model, ""))
	report, err := os.ReadFile(filepath.Join(outbox, "REPORT.md"))
	require.NoError(t, err)
	result, err := os.ReadFile(filepath.Join(outbox, "RESULT.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(report), "input=0")
	assert.Contains(t, string(report), "(opencode: -)")
	assert.NotContains(t, string(result), "$0.00")
	assert.Equal(t, 1, strings.Count(string(report), "Cost: "))
	assert.Equal(t, 1, strings.Count(string(result), "tokens: "))
}

// The reconcile lists the unpriced runs per friend and route, one line each with the
// count, so the seat sees the hole on one line.
func TestReconcileUnpricedListsTheHole(t *testing.T) {
	t.Parallel()
	runs := []UnpricedRun{
		{Friend: "zoe", Route: "flash-mercury"},
		{Friend: "bob", Route: "flash-mercury"},
		{Friend: "zoe", Route: "flash-mercury"},
		{Friend: "bob", Route: ""},
		{Friend: "zoe", Route: ""},
	}
	got := ReconcileUnpriced(runs)
	assert.Equal(t, []string{
		"friend=bob route=- unpriced=1",
		"friend=bob route=flash-mercury unpriced=1",
		"friend=zoe route=- unpriced=1",
		"friend=zoe route=flash-mercury unpriced=2",
	}, got)
	assert.Empty(t, ReconcileUnpriced(nil), "no runs, no hole")
}
