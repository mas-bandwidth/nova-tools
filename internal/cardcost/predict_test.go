package cardcost

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The predicted cost (predict.go): each class's tokens times its price per million,
// exactly, and no prediction (never a zero) where the run cannot be priced.

// sheet is a price sheet with every token price set and reasoning billed as output.
func sheet() Prices {
	return Prices{Input: "0.3", CacheRead: "0.03", CacheWrite: "0.375", Output: "1.2", ReasoningAsOutput: true}
}

// run is a run's tokens with every class reported, the rest unreported.
func run(in, cr, cw, out, rs int64) Tokens {
	t := None()
	t.Input, t.CacheRead, t.CacheWrite, t.Output, t.Reasoning = in, cr, cw, out, rs
	return t
}

func TestPredictPricesEachClassExactly(t *testing.T) {
	t.Parallel()
	with := func(f func(*Prices)) Prices { p := sheet(); f(&p); return p }
	withTokens := func(t Tokens, f func(*Tokens)) Tokens { f(&t); return t }
	cases := []struct {
		name string
		t    Tokens
		p    Prices
		want Prediction
	}{
		{name: "uncached input alone", t: run(1_000_000, 0, 0, 0, 0), p: sheet(), want: Prediction{USD: "0.3"}},
		{name: "cache read alone", t: run(0, 1_000_000, 0, 0, 0), p: sheet(), want: Prediction{USD: "0.03"}},
		{name: "cache write alone", t: run(0, 0, 1_000_000, 0, 0), p: sheet(), want: Prediction{USD: "0.375"}},
		{name: "output alone", t: run(0, 0, 0, 1_000_000, 0), p: sheet(), want: Prediction{USD: "1.2"}},
		{name: "reasoning billed as output", t: run(0, 0, 0, 0, 1_000_000), p: sheet(), want: Prediction{USD: "1.2"}},
		{name: "reasoning not billed", t: run(0, 0, 0, 1000, 1_000_000), p: with(func(p *Prices) { p.ReasoningAsOutput = false }), want: Prediction{USD: "0.0012"}},
		{name: "every class at once", t: run(19541, 36336, 0, 692, 70), p: sheet(), want: Prediction{USD: "0.00786678"}},
		{name: "one token is a millionth of the price", t: run(1, 0, 0, 0, 0), p: sheet(), want: Prediction{USD: "0.0000003"}},
		{name: "a price past float64's digits stays exact", t: run(3, 0, 0, 0, 0), p: with(func(p *Prices) { p.Input = "0.10000000000000000000000000001" }),
			want: Prediction{USD: "0.00000030000000000000000000000000003"}},
		{name: "a class with no token needs no price", t: run(1000, 0, 0, 0, 0), p: Prices{Input: "2"}, want: Prediction{USD: "0.002"}},
		{name: "a class with tokens and no price", t: run(1000, 5, 0, 0, 0), p: Prices{Input: "2"}, want: Prediction{Why: WhyNoPrice + "cache_read"}},
		{name: "no price sheet", t: run(1000, 0, 0, 0, 0), p: Prices{ReasoningAsOutput: true, Billing: BillingMetered}, want: Prediction{Why: WhyNoSheet}},
		{name: "no token reported", t: None(), p: sheet(), want: Prediction{Why: WhyNoTokens}},
		{name: "a prompt under the threshold: base prices", t: withTokens(run(100_000, 0, 0, 1_000_000, 0), func(t *Tokens) { t.MaxPrompt = 128_000 }),
			p: with(func(p *Prices) { p.LongContext, p.InputLong, p.OutputLong = 128_000, "0.6", "2.4" }), want: Prediction{USD: "1.23"}},
		{name: "a prompt over the threshold: long prices for the run", t: withTokens(run(100_000, 0, 0, 1_000_000, 0), func(t *Tokens) { t.MaxPrompt = 128_001 }),
			p: with(func(p *Prices) { p.LongContext, p.InputLong, p.OutputLong = 128_000, "0.6", "2.4" }), want: Prediction{USD: "2.46", Long: true}},
		{name: "a threshold and no prompt size: base prices", t: run(100_000, 0, 0, 1_000_000, 0),
			p: with(func(p *Prices) { p.LongContext, p.InputLong, p.OutputLong = 128_000, "0.6", "2.4" }), want: Prediction{USD: "1.23"}},
		{name: "the per-request fee", t: withTokens(run(0, 0, 0, 1000, 0), func(t *Tokens) { t.Requests = 7 }),
			p: with(func(p *Prices) { p.Request = "0.0000125" }), want: Prediction{USD: "0.0012875"}},
		{name: "a fee and no request count", t: run(0, 0, 0, 1000, 0), p: with(func(p *Prices) { p.Request = "0.01" }), want: Prediction{Why: WhyNoRequests}},
		{name: "a fee of zero needs no count", t: run(0, 0, 0, 1000, 0), p: with(func(p *Prices) { p.Request = "0" }), want: Prediction{USD: "0.0012"}},
		{name: "the gateway percent on the whole", t: withTokens(run(0, 0, 0, 1_000_000, 0), func(t *Tokens) { t.Requests = 1 }),
			p: with(func(p *Prices) { p.Request, p.GatewayPercent = "0.8", "5.5" }), want: Prediction{USD: "2.11"}},
		{name: "a price that is not a decimal", t: run(10, 0, 0, 0, 0), p: with(func(p *Prices) { p.Input = "1e-6" }), want: Prediction{Why: WhyBadPrice + "input"}},
		{name: "the longest prediction a sheet can make is written whole", t: run(1, 0, 0, 0, 0),
			p: with(func(p *Prices) {
				p.Input, p.GatewayPercent = "0.000000000000000000000000000001", "0.000000000000000000000000000001"
			}),
			want: Prediction{USD: "0.00000000000000000000000000000000000100000000000000000000000000000001"}},
		{name: "a plan is priced at the metered prices", t: run(1_000_000, 0, 0, 0, 0), p: with(func(p *Prices) { p.Billing = BillingPlan }), want: Prediction{USD: "0.3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Predict(tc.t, tc.p))
		})
	}
}

func TestTheSheetsCopyOnACardReadsBackAsTheSheet(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		p    Prices
		copy string
	}{
		{name: "the token prices", p: sheet(), copy: "in:0.3,cr:0.03,cw:0.375,out:1.2,ro:true"},
		{name: "every field but the source", p: Prices{Input: "3", Output: "15", ReasoningAsOutput: false, LongContext: 200000, InputLong: "6", OutputLong: "22.5",
			Request: "0.001", Billing: BillingPlan, GatewayPercent: "5.5", AsOf: "2026-10-01"},
			copy: "in:3,out:15,ro:false,long:200000,inl:6,outl:22.5,req:0.001,bill:plan,gw:5.5,asof:2026-10-01"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.copy, tc.p.Copy())
			assert.Equal(t, tc.p, ParseCopy(tc.copy))
		})
	}
	withSource := sheet()
	withSource.Source = "https://example.com/pricing"
	assert.Equal(t, sheet().Copy(), withSource.Copy(), "the source stays on the route's history")
}

func TestPricesOfReadsTheRoutesFields(t *testing.T) {
	t.Parallel()
	p := PricesOf(map[string]string{FieldInput: "0.27", FieldOutput: "1.1", FieldReasoningAsOutput: "false", FieldLongContext: "128000",
		FieldBilling: BillingPlan, FieldSource: "https://example.com/pricing"})
	assert.Equal(t, Prices{Input: "0.27", Output: "1.1", LongContext: 128000, Billing: BillingPlan, Source: "https://example.com/pricing"}, p)
	assert.True(t, PricesOf(map[string]string{}).ReasoningAsOutput, "a route with no field bills reasoning as output, the default")
	assert.False(t, PricesOf(map[string]string{FieldBilling: BillingMetered}).Priced(), "no price is no sheet")
}
