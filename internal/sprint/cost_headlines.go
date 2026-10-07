package sprint

import (
	"cmp"
	"math/big"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// PriceCoverage is the dollar coverage of a headline, with disjoint sources.
// Actual is the harness's reported figure, not a reconciled provider invoice
// (docs/SPEC-SPRINT.md, cost headline coverage).
type PriceCoverage struct {
	All       int `json:"all"`
	Priced    int `json:"priced"`
	Actual    int `json:"actual"`
	Estimated int `json:"estimated"`
	Unpriced  int `json:"unpriced"`
}

func coverageOf(t cardcost.Total) PriceCoverage {
	return PriceCoverage{All: t.Records, Priced: t.ChargedOf, Actual: t.ActualOf,
		Estimated: max(0, t.ChargedOf-t.ActualOf), Unpriced: max(0, t.Records-t.ChargedOf)}
}

func (c *PriceCoverage) add(v PriceCoverage) {
	c.All += v.All
	c.Priced += v.Priced
	c.Actual += v.Actual
	c.Estimated += v.Estimated
	c.Unpriced += v.Unpriced
}

// CostHeadline compares completed recorded runs only, across work and reads.
// Its average is over priced runs; unknown runs never dilute that denominator.
type CostHeadline struct {
	USD       string        `json:"usd,omitempty"`
	Coverage  PriceCoverage `json:"coverage"`
	PerPriced string        `json:"per_priced"`
	Stage     string        `json:"stage"`
	Scope     string        `json:"scope"`
}

func (h *CostHeadline) add(u cardcost.Usage) {
	h.Coverage.add(coverageOf(cardcost.NoTotal().Add(u)))
	if usd := cmp.Or(u.Actual, u.Predicted); usd != "" {
		if sum, ok := cardcost.Sum(h.USD, usd); ok {
			h.USD = sum
		}
	}
}

func (h CostHeadline) finish() CostHeadline {
	h.Stage = "completed-recorded-runs"
	h.Scope = "work-and-reads; unattributed-runs-separate; actual=harness-reported; estimated=route-prices; excludes-provider-gap"
	h.PerPriced = perPriced(h.USD, h.Coverage.Priced)
	h.USD = MoneyText(h.USD)
	return h
}

func perPriced(usd string, n int) string {
	r, err := amountOf(usd)
	if n <= 0 || err != nil || r == nil {
		return "-"
	}
	return cardcost.Cents(r.Quo(r, big.NewRat(int64(n), 1)))
}

// pricedOutcome classifies a landed lineage only when every recorded run is
// dollar priced. A legacy landed dollar field has no source breakdown and is
// estimated coverage, never attributed as a reported actual.
func pricedOutcome(v CardCostView, legacy string) (usd string, actual, priced bool) {
	t := v.Total
	if t.Records == 0 {
		if r, err := amountOf(legacy); err == nil && r != nil {
			return legacy, false, true
		}
		return "", false, false
	}
	return t.Charged, t.ActualOf == t.Records, t.ChargedOf == t.Records && t.Charged != ""
}
