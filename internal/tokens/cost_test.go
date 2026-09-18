package tokens

import "testing"

func TestCostNodeAggregation(t *testing.T) {
	receipts := []CostReceiptRow{
		{NodeID: "a", Stage: "builder", PricedTokens: 1000, UnpricedTokens: 200, Usd: 50_000},  // $0.05
		{NodeID: "a", Stage: "review", PricedTokens: 300, UnpricedTokens: 0, Usd: 15_000},        // $0.015
		{NodeID: "a", Stage: "repair", PricedTokens: 0, UnpricedTokens: 500, Usd: 0},             // no rate
		{NodeID: "b", Stage: "builder", PricedTokens: 9999, UnpricedTokens: 9999, Usd: 999_999}, // another node
	}

	sum := AggregateNodeCost(receipts, "a")
	if sum.NodeID != "a" {
		t.Errorf("NodeID = %q, want a", sum.NodeID)
	}
	if sum.PricedTokens != 1300 {
		t.Errorf("PricedTokens = %d, want 1300", sum.PricedTokens)
	}
	if sum.UnpricedTokens != 700 {
		t.Errorf("UnpricedTokens = %d, want 700", sum.UnpricedTokens)
	}
	if sum.Tokens != 2000 {
		t.Errorf("Tokens = %d, want 2000", sum.Tokens)
	}
	if sum.Usd != 65_000 {
		t.Errorf("Usd = %d, want 65000", sum.Usd)
	}
	if want := 65000.0 / 2000.0; sum.PerMtok != want {
		t.Errorf("PerMtok = %f, want %f", sum.PerMtok, want)
	}

	got := FormatNodeCost(sum)
	want := "tokens=2000 usd=0.0650 per_mtok=32.5000 priced_tokens=1300 unpriced_tokens=700"
	if got != want {
		t.Errorf("FormatNodeCost = %q, want %q", got, want)
	}

	// per_mtok is 0.0000 over no tokens, never a division.
	zero := AggregateNodeCost(receipts, "missing")
	if zero.Tokens != 0 || zero.Usd != 0 || zero.PricedTokens != 0 || zero.UnpricedTokens != 0 {
		t.Errorf("non-existent node did not return zero values: %+v", zero)
	}
	if zero.PerMtok != 0 {
		t.Errorf("PerMtok over no tokens = %f, want 0", zero.PerMtok)
	}
	if got := FormatNodeCost(zero); got != "tokens=0 usd=0.0000 per_mtok=0.0000 priced_tokens=0 unpriced_tokens=0" {
		t.Errorf("FormatNodeCost over zero = %q", got)
	}

	// Empty receipts return zero values without error.
	empty := AggregateNodeCost(nil, "a")
	if empty.Tokens != 0 || empty.Usd != 0 || empty.PricedTokens != 0 || empty.UnpricedTokens != 0 || empty.PerMtok != 0 {
		t.Errorf("empty receipts did not return zero values: %+v", empty)
	}
}
