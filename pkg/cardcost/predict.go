package cardcost

import (
	"math/big"
	"slices"
)

// Unreported is a token count, request count or prompt size the harness did not
// report: an absence, never a zero.
const Unreported int64 = -1

// Tokens is what one run spent, by class, as the harness reported it: uncached
// input, cached input read, cache write, output and reasoning (opencode reports
// reasoning apart from output), the requests made, and the largest prompt of one
// request (its input, cache read and cache write). Each is Unreported when the
// harness did not say.
type Tokens struct {
	Input      int64 `json:"input"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Output     int64 `json:"output"`
	Reasoning  int64 `json:"reasoning"`
	Requests   int64 `json:"requests"`
	MaxPrompt  int64 `json:"max_prompt"`
}

// None is a run of which nothing was reported.
func None() Tokens {
	return Tokens{Input: Unreported, CacheRead: Unreported, CacheWrite: Unreported, Output: Unreported, Reasoning: Unreported,
		Requests: Unreported, MaxPrompt: Unreported}
}

// Reported says the harness reported a token class of the run.
func (t Tokens) Reported() bool {
	return slices.ContainsFunc([]int64{t.Input, t.CacheRead, t.CacheWrite, t.Output, t.Reasoning}, func(n int64) bool { return n >= 0 })
}

// Total is the run's tokens over the classes reported.
func (t Tokens) Total() int64 {
	var sum int64
	for _, n := range []int64{t.Input, t.CacheRead, t.CacheWrite, t.Output, t.Reasoning} {
		if n > 0 {
			sum += n
		}
	}
	return sum
}

// Prediction is a run's predicted cost under a price sheet: USD the exact decimal,
// "" when the run cannot be priced, and Why says why not. Long is true when a
// request's prompt was above the sheet's threshold and the run was priced at the
// long input and output prices.
type Prediction struct {
	USD  string `json:"usd"`
	Long bool   `json:"long,omitempty"`
	Why  string `json:"why,omitempty"`
}

// Why a run has no prediction, one word each, as the usage record keeps it
// (unpriced=<why>): no route with a price sheet, no token reported, a class with
// tokens and no price (no-price:<class>), a price that is not a decimal
// (bad-price:<class>), a per-request fee with no request count, and no route found
// for the run at all.
const (
	WhyNoSheet    = "no-price-sheet"
	WhyNoTokens   = "no-tokens"
	WhyNoPrice    = "no-price:"
	WhyBadPrice   = "bad-price:"
	WhyNoRequests = "no-request-count"
	WhyNoRoute    = "no-route"
)

// million is the tokens a price is for.
var million = big.NewRat(1_000_000, 1)

// Predict prices the run's tokens by the sheet (docs/SPEC-SPRINT.md, "What a card
// cost"): each class's tokens times its price per million; reasoning at the output
// price when the sheet bills it as output, else not billed; plus the per-request fee
// times the requests; all times one plus the gateway percent. When the sheet has a
// long-context threshold and the run's largest prompt is above it, input and output
// are priced at the long prices: the harness reports the run's sums, not each
// request's, so a run that crossed the threshold once is priced long as a whole (an
// upper bound, marked Long). A sheet with no price, a run with no token reported, a
// class with tokens and no price, or a fee with no request count gives no
// prediction, never a zero.
func Predict(t Tokens, p Prices) Prediction {
	if !p.Priced() {
		return Prediction{Why: WhyNoSheet}
	}
	if !t.Reported() {
		return Prediction{Why: WhyNoTokens}
	}
	in, out := p.Input, p.Output
	long := p.LongContext > 0 && t.MaxPrompt > p.LongContext
	if long {
		in, out = p.InputLong, p.OutputLong
	}
	output := t.Output
	if p.ReasoningAsOutput && t.Reasoning > 0 {
		output = max(output, 0) + t.Reasoning
	}
	sum := new(big.Rat)
	for _, c := range []struct {
		class string
		n     int64
		price string
	}{{"input", t.Input, in}, {"cache_read", t.CacheRead, p.CacheRead}, {"cache_write", t.CacheWrite, p.CacheWrite}, {"output", output, out}} {
		if c.n <= 0 {
			continue
		}
		if c.price == "" {
			return Prediction{Long: long, Why: WhyNoPrice + c.class}
		}
		r, err := Decimal(c.price)
		if err != nil {
			return Prediction{Long: long, Why: WhyBadPrice + c.class}
		}
		sum.Add(sum, new(big.Rat).Quo(new(big.Rat).Mul(big.NewRat(c.n, 1), r), million))
	}
	if p.Request != "" {
		fee, err := Decimal(p.Request)
		if err != nil {
			return Prediction{Long: long, Why: WhyBadPrice + "request"}
		}
		if fee.Sign() > 0 {
			if t.Requests < 0 {
				return Prediction{Long: long, Why: WhyNoRequests}
			}
			sum.Add(sum, new(big.Rat).Mul(big.NewRat(t.Requests, 1), fee))
		}
	}
	if p.GatewayPercent != "" {
		gw, err := Decimal(p.GatewayPercent)
		if err != nil {
			return Prediction{Long: long, Why: WhyBadPrice + "gateway"}
		}
		sum.Mul(sum, new(big.Rat).Add(big.NewRat(1, 1), new(big.Rat).Quo(gw, big.NewRat(100, 1))))
	}
	return Prediction{USD: Text(sum), Long: long}
}
