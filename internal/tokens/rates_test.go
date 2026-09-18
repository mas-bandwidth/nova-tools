package tokens

import (
	"math"
	"strings"
	"testing"
)

// TestRatesPricingAndUnpriced pins the rates table's four moves: parsing the four-column
// price list, pricing a known model by its input and output counts, refusing to price an
// unknown model, and reporting the earliest verified date in the table.
func TestRatesPricingAndUnpriced(t *testing.T) {
	table := strings.Join([]string{
		"model\tinput_rate\toutput_rate\tverified_date",
		"claude-x\t0.0000030\t0.000015\t2026-09-01",
		"mercury-2.5\t0.0000020\t0.000010\t2026-08-15",
	}, "\n") + "\n"

	rt, err := ParseRatesTable(strings.NewReader(table))
	if err != nil {
		t.Fatalf("a four-column table does not parse: %v", err)
	}

	// A known model is priced: input and output tokens each weigh their own rate.
	cost, priced := rt.CalculateCost("claude-x", 1_000_000, 0)
	if !priced {
		t.Fatal("claude-x is not priced")
	}
	if want := 3.0; math.Abs(cost-want) > 1e-9 {
		t.Errorf("1M input tokens of claude-x cost %v, want %v", cost, want)
	}

	cost, priced = rt.CalculateCost("claude-x", 1000, 2000)
	if !priced {
		t.Fatal("claude-x is not priced")
	}
	if want := 1000*0.0000030 + 2000*0.000015; math.Abs(cost-want) > 1e-9 {
		t.Errorf("claude-x 1000 in / 2000 out cost %v, want %v", cost, want)
	}

	// An unknown model is unpriced, not free: cost is zero and the priced flag is false.
	cost, priced = rt.CalculateCost("no-such-model", 100, 100)
	if priced {
		t.Errorf("an unknown model is marked priced, cost %v", cost)
	}
	if cost != 0.0 {
		t.Errorf("an unknown model has cost %v, want 0.0", cost)
	}

	// The earliest verified date is the table's one stale price.
	if got := rt.OldestVerifiedDate(); got != "2026-08-15" {
		t.Errorf("oldest verified date is %q, want 2026-08-15", got)
	}
}
