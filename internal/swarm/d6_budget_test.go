package swarm

import "testing"

// DOGFOOD D6 (2026-09-11, HIGH): a real job hit 4.18M of a 4,000,000-token budget in under
// three minutes and was ended with ZERO findings. It had not spent four million tokens: the
// budget summed all five of nova-tokens's columns, and CACHE READS are most of them -- the
// same context re-read on every turn, not tokens the job spent doing its work.
//
// Rule 13's budget is a stop condition on the job's SPEND: what it sent, what it generated,
// and what it reasoned. A cache read is the provider re-reading what it already has, and
// counting it ends a long job in minutes while the true spend is a fraction of the number.
// The usage ROW (rule 12) still carries all five columns -- nothing is hidden, and `cost`
// reads what it always read.
func TestTheBudgetCountsSpendNotCacheReads(t *testing.T) {
	// The shape of the real job: a small spend under a huge re-read.
	u := ProviderUsage{Observed: true, Values: map[string]string{
		"tokens_in": "11139", "tokens_out": "944", "cache_write": "3000",
		"cache_read": "4160000", "reasoning": "120",
	}}
	spent, seen, partial := u.Budget()
	if spent != 11139+944+120 {
		t.Errorf("the budget counts input, output and reasoning: spent=%d, want %d", spent, 11139+944+120)
	}
	if seen != 3 || partial {
		t.Errorf("three columns observed and none missing: seen=%d partial=%t", seen, partial)
	}
	// The row keeps every column, and the whole-row sum is unchanged.
	if sum, _, _ := u.Sum(); sum != 11139+944+3000+4160000+120 {
		t.Errorf("the usage row still carries all five columns, got sum=%d", sum)
	}
	// A partial observation still prints the plus: a budget missing a column it wants is
	// not a whole one.
	partialUsage := ProviderUsage{Observed: true, Values: map[string]string{
		"tokens_in": "100", "tokens_out": Dash, "cache_read": "999999", "reasoning": Dash,
	}}
	spent, seen, partial = partialUsage.Budget()
	if spent != 100 || seen != 1 || !partial {
		t.Errorf("a partial budget observation counts what it has and says so: spent=%d seen=%d partial=%t, want 100, 1, true", spent, seen, partial)
	}
}

// AND THE COLUMNS THEMSELVES, so that a future edit that adds cache_read back has to say so
// in this list and turn this test red.
func TestBudgetColumnsAreSpendOnly(t *testing.T) {
	want := []string{"tokens_in", "tokens_out", "reasoning"}
	if len(BudgetColumns) != len(want) {
		t.Fatalf("the budget counts %v, got %v", want, BudgetColumns)
	}
	for i, c := range want {
		if BudgetColumns[i] != c {
			t.Errorf("budget column %d is %q, want %q", i, BudgetColumns[i], c)
		}
	}
}
