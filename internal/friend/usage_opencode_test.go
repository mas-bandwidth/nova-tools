package friend

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A finish's usage is read from opencode's own session record, never the model's
// report (docs/SPEC-FRIEND.md, usage capture per adapter): the tokens summed over
// the record's steps, and the provider and model the steps ran.

// A session's record sums to the finish's usage: its assistant steps' tokens by
// class, and the model from the same record.
func TestSumOpenCodeRecordSumsTheSessionsTokensAndModel(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("testdata", "opencode-session.json"))
	require.NoError(t, err)
	u, err := SumOpenCodeRecord(raw)
	require.NoError(t, err)
	assert.Equal(t, cardcost.Tokens{Input: 150, CacheRead: 30, CacheWrite: 40, Output: 30, Reasoning: 5, Requests: cardcost.Unreported, MaxPrompt: cardcost.Unreported}, u.Tokens)
	assert.Equal(t, "inception/mercury-2.5", u.Model)

	// a record with no assistant step carrying tokens is a usage that cannot be read
	_, err = SumOpenCodeRecord([]byte(`{"messages":[{"info":{"role":"user"}}]}`))
	assert.Error(t, err)
	_, err = SumOpenCodeRecord([]byte(`not json`))
	assert.Error(t, err)
}

// A record whose modelID carries no provider keeps the provider from providerID, so the
// finish's model is "provider/model" whatever the record's fields say (the provider and
// model come from the same record).
func TestSumOpenCodeRecordKeepsTheProviderWithTheModel(t *testing.T) {
	t.Parallel()
	u, err := SumOpenCodeRecord([]byte(`{"messages":[{"info":{"role":"assistant","modelID":"mercury-2.5","providerID":"inception","tokens":{"input":1,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}}}]}`))
	require.NoError(t, err)
	assert.Equal(t, "inception/mercury-2.5", u.Model)

	// a modelID that already carries the provider is left as it is
	u, err = SumOpenCodeRecord([]byte(`{"messages":[{"info":{"role":"assistant","modelID":"inception/mercury-2.5","providerID":"inception","tokens":{"input":1,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}}}]}`))
	require.NoError(t, err)
	assert.Equal(t, "inception/mercury-2.5", u.Model)
}

// A session record the adapter cannot read is finished usage=unknown, and the seat
// is told once with the card and why, never priced as free.
func TestAMissingSessionRecordIsUsageUnknownWithTheJudgment(t *testing.T) {
	t.Parallel()
	p := &OpenCodePriced{OpenCode: &OpenCode{Dir: t.TempDir()}, DataDir: t.TempDir()}
	_, err := p.SessionUsage("ses_missing")
	require.Error(t, err)
	subject, body := UsageUnknownNote("bob", "c1", err.Error())
	assert.Equal(t, "friend bob: usage unknown for card c1", subject)
	assert.Contains(t, body, "usage unknown for card c1")
	assert.Contains(t, body, "ses_missing")
	assert.Contains(t, body, "never $0.00")
	assert.NotContains(t, subject, "$")

	// no data directory is an unknown usage too
	_, err = (&OpenCodePriced{OpenCode: &OpenCode{Dir: t.TempDir()}}).SessionUsage("ses_1")
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

	// a usage that could not be read says so, never a dollar figure, and its
	// tokens read unreported, never as zeros
	_, tokens, cost = CostLineOf(LaneTokens{Tokens: cardcost.None()}, rp, "inception/mercury-2.5", "opencode session record ses_x: not found")
	assert.Contains(t, cost, "unpriced (usage unknown:")
	assert.NotContains(t, cost, "$")
	assert.Contains(t, tokens, "input=unreported")

	// a route with real tokens is priced as before
	assert.Equal(t, "$0.25", FinishCost(cardcost.Tokens{Input: 1_000_000, Requests: cardcost.Unreported, MaxPrompt: cardcost.Unreported}, RoutePrice{Name: "flash-mercury", Found: true, Prices: cardcost.Prices{Input: "0.25"}}, "inception/mercury-2.5"))
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
