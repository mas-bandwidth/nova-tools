package main

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// THE DOLLAR BUDGET (nova-tools #5094). Mercury re-sends its whole context every turn with
// no cache, so a 400,000-token budget ended two of its cards at two cents each; the budget
// exists to bound cost, so a route can bound it in dollars: the harness's own reported cost,
// sampled live, at or past --usd ends the card, named `stopped=usd`. A token budget beside
// it still ends the card first when it is reached first.
func TestTheDollarBudgetStopsTheCardAtItsCost(t *testing.T) {
	t.Parallel()
	reading := func(in, cost string) swarm.ProviderUsage {
		v := map[string]string{"tokens_in": in, "tokens_out": "0", "reasoning": "0"}
		if cost != "" {
			v["cost"] = cost
		}
		return swarm.ProviderUsage{Values: v, Observed: true}
	}
	usd := big.NewRat(1, 2)
	for name, c := range map[string]struct {
		tokens int
		usd    *big.Rat
		read   swarm.ProviderUsage
		want   string
	}{
		"under the dollar budget, past the old token count": {0, usd, reading("509940", "0.0211"), ""},
		"at the dollar budget":                              {0, usd, reading("9000000", "0.5"), stoppedUSD},
		"past it":                                           {2000000, usd, reading("9000000", "0.51"), stoppedUSD},
		"no cost reported: the dollar budget cannot fire":   {0, usd, reading("9000000", ""), ""},
		"the token budget reached first still names itself": {400000, usd, reading("509940", "0.0211"), stoppedTokens},
	} {
		s := startLiveSampler("", 0, nativeRunConfig{tokens: c.tokens, usd: c.usd}, "")
		s.fold(c.read, nil, 0)
		got := ""
		select {
		case got = <-s.Fired():
		default:
		}
		assert.Equal(t, c.want, got, name)
	}
	// and the test before any relaunch asks the job's final cost too
	s := startLiveSampler("", 0, nativeRunConfig{usd: usd}, "")
	assert.Equal(t, stoppedUSD, s.StopWordAtFinal(10, true, "0.75"), "a first launch that spent the dollar budget is not launched again")
	s = startLiveSampler("", 0, nativeRunConfig{usd: usd}, "")
	assert.Empty(t, s.StopWordAtFinal(10, true, "0.25"))
}

// The line a dollar budget's stop prints names it as the brief's own words: "$0.51 of $0.50".
func TestTheDollarBudgetLineNamesTheCostAndTheBudget(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "$0.51 of $0.50, tokens 9,000,000", nativeBudgetWords(stoppedUSD, 0, 9000000, false, "0.5003", "0.5"))
	assert.Equal(t, "$0.50 of $0.50, tokens 9,000,000 of 10,000,000", nativeBudgetWords(stoppedUSD, 10000000, 9000000, false, "0.5", "0.5"))
}
