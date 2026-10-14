package cardcost

import (
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Decimal reads digits with at most one point and at most MaxFraction digits after it: no
// sign, no exponent, no thousands separator, no bare point; Canonical is the one spelling.
func TestDecimalAndCanonicalReadOneSpelling(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{"0.30", "0.3"}, {"15", "15"}, {"0.0000125", "0.0000125"}, {"007", "7"}, {"1.0", "1"}, {"0.0", "0"},
		{"00.10", "0.1"}, {"0", "0"}, {"", ""},
		{"0." + strings.Repeat("0", MaxFraction-1) + "1", "0." + strings.Repeat("0", MaxFraction-1) + "1"},
	} {
		got, err := Canonical(tc.in)
		require.NoError(t, err, "%q", tc.in)
		assert.Equal(t, tc.want, got, "%q", tc.in)
	}
	for _, bad := range []string{".5", "-1", "+1", "1e3", "1,000", "1.", "1.2.3", " 1", "1 ", "abc", "0x10", "0." + strings.Repeat("1", MaxFraction+1)} {
		_, err := Decimal(bad)
		assert.Error(t, err, "%q", bad)
		_, err = Canonical(bad)
		assert.Error(t, err, "%q", bad)
	}
	_, err := Decimal("0." + strings.Repeat("1", MaxFraction+1))
	assert.Contains(t, err.Error(), "want at most 30 digits after the point")
	_, err = Decimal("-1")
	assert.Contains(t, err.Error(), "want a non-negative decimal like 0.30")
}

// Text writes a terminating rational exactly with no trailing zero, and cuts one that does
// not terminate at maxDigits; Cents rounds up to the next cent for display only.
func TestTextIsExactAndCentsRoundsUp(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "0.125", Text(big.NewRat(1, 8)))
	assert.Equal(t, "7", Text(big.NewRat(7, 1)))
	assert.Equal(t, "0", Text(new(big.Rat)))
	assert.Equal(t, "0.0009765625", Text(big.NewRat(1, 1024)))
	assert.Equal(t, "0.00032", Text(big.NewRat(1, 3125)))
	assert.Equal(t, "2.5", Text(big.NewRat(5, 2)))
	third := Text(big.NewRat(1, 3))
	assert.Equal(t, "0."+strings.Repeat("3", maxDigits), third, "a non-terminating decimal is cut at maxDigits")
	assert.Equal(t, "0."+strings.Repeat("6", maxDigits-1)+"7", Text(big.NewRat(2, 3)), "and rounded by FloatString")
	for _, tc := range []struct {
		num, den int64
		want     string
	}{
		{12345, 10000, "$1.24"}, {202111, 10000, "$20.22"}, {12, 10, "$1.20"}, {0, 1, "$0.00"}, {1, 1000, "$0.01"},
		{123, 100, "$1.23"}, {1, 1, "$1.00"}, {999, 1000, "$1.00"}, {1000001, 1000000, "$1.01"},
	} {
		assert.Equal(t, tc.want, Cents(big.NewRat(tc.num, tc.den)), "%d/%d", tc.num, tc.den)
	}
}

// Sum adds decimal strings exactly, "" left out, and is false on one that is no decimal;
// it accepts a stored decimal of any length, where Decimal would refuse it at input.
func TestSumIsExactAndSkipsEmpties(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{[]string{"0.1", "0.2"}, "0.3"},
		{[]string{"1", "", "2"}, "3"},
		{nil, "0"},
		{[]string{""}, "0"},
		{[]string{"0.30", "0.70"}, "1"},
		{[]string{"0." + strings.Repeat("0", MaxFraction) + "1"}, "0." + strings.Repeat("0", MaxFraction) + "1"},
	} {
		got, ok := Sum(tc.in...)
		require.True(t, ok, "%v", tc.in)
		assert.Equal(t, tc.want, got, "%v", tc.in)
	}
	for _, bad := range [][]string{{"x"}, {"1", "-1"}, {"1", "1e3"}, {".5"}} {
		got, ok := Sum(bad...)
		assert.False(t, ok, "%v", bad)
		assert.Equal(t, "", got)
	}
}

// Tokens: None reports nothing; a reported zero is a report; Requests and MaxPrompt are no
// token class; Total adds the classes reported, counting an unreported class as nothing.
func TestTokensReportedAndTotal(t *testing.T) {
	t.Parallel()
	assert.False(t, None().Reported())
	assert.Equal(t, int64(0), None().Total())
	zero := None()
	zero.Input = 0
	assert.True(t, zero.Reported(), "a reported zero is a report, not an absence")
	req := None()
	req.Requests, req.MaxPrompt = 3, 100
	assert.False(t, req.Reported(), "requests and the largest prompt are no token class")
	assert.Equal(t, int64(0), req.Total())
	some := None()
	some.Input, some.Output, some.Reasoning, some.Requests = 10, 5, 2, 99
	assert.True(t, some.Reported())
	assert.Equal(t, int64(17), some.Total())
	assert.Equal(t, Unreported, int64(-1))
}

// PricesOf: a missing field is not set; reasoning is billed as output unless the field says
// false (an unreadable value is true); an unreadable long_context is none. Priced is any
// price field set, the billing kind and the gateway alone are no price.
func TestPricesOfAndPriced(t *testing.T) {
	t.Parallel()
	p := PricesOf(map[string]string{FieldInput: "0.27", FieldOutput: "1.1", FieldLongContext: "128000", FieldReasoningAsOutput: "false",
		FieldRequest: "0.01", FieldBilling: BillingPlan, FieldGateway: "5", FieldSource: "the sheet", FieldAsOf: "2026-10-12"})
	assert.Equal(t, Prices{Input: "0.27", Output: "1.1", LongContext: 128000, ReasoningAsOutput: false, Request: "0.01", Billing: BillingPlan,
		GatewayPercent: "5", Source: "the sheet", AsOf: "2026-10-12"}, p)
	assert.True(t, PricesOf(map[string]string{}).ReasoningAsOutput, "reasoning is billed as output unless the field says false")
	assert.True(t, PricesOf(map[string]string{FieldReasoningAsOutput: "no"}).ReasoningAsOutput, "an unreadable bool is true")
	assert.False(t, PricesOf(map[string]string{FieldReasoningAsOutput: "0"}).ReasoningAsOutput)
	assert.Equal(t, int64(0), PricesOf(map[string]string{FieldLongContext: "lots"}).LongContext)
	assert.False(t, Prices{}.Priced())
	assert.False(t, Prices{Billing: BillingMetered, GatewayPercent: "5", Source: "x", AsOf: "y", LongContext: 1, ReasoningAsOutput: true}.Priced())
	for _, p := range []Prices{{Input: "1"}, {CacheRead: "1"}, {CacheWrite: "1"}, {Output: "1"}, {InputLong: "1"}, {OutputLong: "1"}, {Request: "1"}} {
		assert.True(t, p.Priced(), "%+v", p)
	}
	assert.Equal(t, []string{"metered", "plan", "subscription"}, Billings)
}

// Copy is the sheet as a card keeps it: one word, the keys in order, a part left out when
// not set or when its value would break the word; the source never rides along.
func TestCopyIsOneWordInKeyOrder(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "in:0.27,cr:0.07,out:1.1,ro:false", Prices{Input: "0.27", CacheRead: "0.07", Output: "1.1"}.Copy())
	assert.Equal(t, "ro:false", Prices{}.Copy())
	full := Prices{Input: "1", CacheRead: "2", CacheWrite: "3", Output: "4", ReasoningAsOutput: true, LongContext: 128000, InputLong: "5",
		OutputLong: "6", Request: "0.01", Billing: BillingMetered, GatewayPercent: "5", Source: "the sheet", AsOf: "2026-10-12"}
	assert.Equal(t, "in:1,cr:2,cw:3,out:4,ro:true,long:128000,inl:5,outl:6,req:0.01,bill:metered,gw:5,asof:2026-10-12", full.Copy())
	assert.NotContains(t, full.Copy(), "the sheet")
	assert.Equal(t, "in:1,ro:true", Prices{Input: "1", ReasoningAsOutput: true, AsOf: "2026 10 12", Billing: "a,b"}.Copy(), "a value with a blank or a comma is left out")
	assert.Len(t, strings.Fields(full.Copy()), 1)
}

// Predict prices each class's tokens at its price per million, reasoning at the output
// price when billed as output, plus the per-request fee, times one plus the gateway
// percent, at the long prices when the largest prompt is above the threshold; a sheet with
// no price, a run with no token, a class with tokens and no price, a price that is no
// decimal, or a fee with no request count gives no prediction, never a zero.
func TestPredictPricesByClassAndRefusesToGuess(t *testing.T) {
	t.Parallel()
	tok := func(in, cr, cw, out, rs, req, maxp int64) Tokens {
		return Tokens{Input: in, CacheRead: cr, CacheWrite: cw, Output: out, Reasoning: rs, Requests: req, MaxPrompt: maxp}
	}
	u := Unreported
	for _, tc := range []struct {
		name string
		t    Tokens
		p    Prices
		want Prediction
	}{
		{"no sheet", tok(1, u, u, u, u, u, u), Prices{}, Prediction{Why: WhyNoSheet}},
		{"no tokens", None(), Prices{Input: "1"}, Prediction{Why: WhyNoTokens}},
		{"requests alone are no tokens", tok(u, u, u, u, u, 3, u), Prices{Input: "1"}, Prediction{Why: WhyNoTokens}},
		{"a million input at 0.27", tok(1_000_000, u, u, u, u, u, u), Prices{Input: "0.27"}, Prediction{USD: "0.27"}},
		{"half a million input", tok(500_000, u, u, u, u, u, u), Prices{Input: "0.27"}, Prediction{USD: "0.135"}},
		{"a reported zero is priced zero", tok(0, u, u, u, u, u, u), Prices{Input: "0.27"}, Prediction{USD: "0"}},
		{"every class", tok(1_000_000, 2_000_000, 3_000_000, 4_000_000, u, u, u), Prices{Input: "1", CacheRead: "0.1", CacheWrite: "0.01", Output: "2"}, Prediction{USD: "9.23"}},
		{"output with no output price", tok(1, u, u, 1, u, u, u), Prices{Input: "1"}, Prediction{Why: WhyNoPrice + "output"}},
		{"cache read with no price", tok(u, 1, u, u, u, u, u), Prices{Input: "1"}, Prediction{Why: WhyNoPrice + "cache_read"}},
		{"input price that is no decimal", tok(1, u, u, u, u, u, u), Prices{Input: "abc"}, Prediction{Why: WhyBadPrice + "input"}},
		{"reasoning billed as output", tok(u, u, u, 10, 5, u, u), Prices{Output: "1", ReasoningAsOutput: true}, Prediction{USD: "0.000015"}},
		{"reasoning not billed", tok(u, u, u, 10, 5, u, u), Prices{Output: "1"}, Prediction{USD: "0.00001"}},
		{"reasoning with output unreported", tok(u, u, u, u, 5, u, u), Prices{Output: "1", ReasoningAsOutput: true}, Prediction{USD: "0.000005"}},
		{"reasoning alone and not billed is no tokens priced", tok(u, u, u, u, 5, u, u), Prices{Output: "1"}, Prediction{USD: "0"}},
		{"request fee", tok(1_000_000, u, u, u, u, 3, u), Prices{Input: "1", Request: "0.01"}, Prediction{USD: "1.03"}},
		{"request fee with no request count", tok(1_000_000, u, u, u, u, u, u), Prices{Input: "1", Request: "0.01"}, Prediction{Why: WhyNoRequests}},
		{"a zero fee needs no count", tok(1_000_000, u, u, u, u, u, u), Prices{Input: "1", Request: "0"}, Prediction{USD: "1"}},
		{"request fee that is no decimal", tok(1_000_000, u, u, u, u, 3, u), Prices{Input: "1", Request: "x"}, Prediction{Why: WhyBadPrice + "request"}},
		{"gateway percent", tok(1_000_000, u, u, u, u, u, u), Prices{Input: "1", GatewayPercent: "5"}, Prediction{USD: "1.05"}},
		{"gateway on the fee too", tok(1_000_000, u, u, u, u, 1, u), Prices{Input: "1", Request: "1", GatewayPercent: "50"}, Prediction{USD: "3"}},
		{"gateway that is no decimal", tok(1_000_000, u, u, u, u, u, u), Prices{Input: "1", GatewayPercent: "x"}, Prediction{Why: WhyBadPrice + "gateway"}},
		{"long context above the threshold", tok(1_000_000, u, u, 1_000_000, u, u, 1001), Prices{Input: "1", Output: "1", LongContext: 1000, InputLong: "2", OutputLong: "3"}, Prediction{USD: "5", Long: true}},
		{"long context at the threshold is not long", tok(1_000_000, u, u, 1_000_000, u, u, 1000), Prices{Input: "1", Output: "1", LongContext: 1000, InputLong: "2", OutputLong: "3"}, Prediction{USD: "2"}},
		{"long context with the prompt unreported", tok(1_000_000, u, u, u, u, u, u), Prices{Input: "1", LongContext: 1000, InputLong: "2"}, Prediction{USD: "1"}},
		{"long context with no long price", tok(1_000_000, u, u, u, u, u, 1001), Prices{Input: "1", LongContext: 1000}, Prediction{Long: true, Why: WhyNoPrice + "input"}},
		{"no threshold means never long", tok(1_000_000, u, u, u, u, u, 1<<40), Prices{Input: "1", InputLong: "2"}, Prediction{USD: "1"}},
		{"exact tiny amounts", tok(1, 1, 1, 1, u, u, u), Prices{Input: "0.0000125", CacheRead: "0.0000125", CacheWrite: "0.0000125", Output: "0.0000125"}, Prediction{USD: "0.00000000005"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Predict(tc.t, tc.p))
		})
	}
}

// ParseUsage and String are inverse over the documented line; cost= is derived from the
// fields and never read; a negative or unreadable count is unreported; wait and run are
// durations read to the second; words the record does not know are kept in order.
func TestUsageLineRoundTrip(t *testing.T) {
	t.Parallel()
	line := "wall=49.49s budget=20088/400000 input=19541 cache_read=36336 cache_write=0 output=692 reasoning=70 requests=6 max_prompt=9861 " +
		"model=opencode/deepseek-v4-pro actual_usd=0.04219614 actual_by=harness wait=3s run=52s price_route=pro-a " +
		"prices=in:0.27,cr:0.07,out:1.1,ro:true predicted_usd=0.00680779 long=yes unpriced=x extra1 k=v cost=both"
	u := ParseUsage(line)
	assert.Equal(t, Usage{Wall: "49.49s", Budget: "20088/400000", Tokens: Tokens{Input: 19541, CacheRead: 36336, CacheWrite: 0, Output: 692, Reasoning: 70, Requests: 6, MaxPrompt: 9861},
		Model: "opencode/deepseek-v4-pro", Actual: "0.04219614", ActualBy: "harness", Wait: 3, Run: 52, Route: "pro-a", Prices: "in:0.27,cr:0.07,out:1.1,ro:true",
		Predicted: "0.00680779", Long: true, Unpriced: "x", Extra: []string{"extra1", "k=v"}}, u)
	assert.Equal(t, line, u.String())
	assert.Equal(t, CostBoth, u.Present())

	assert.Equal(t, NoUsage(), ParseUsage(""))
	assert.Equal(t, "cost=none", NoUsage().String())
	old := ParseUsage("wall=1s budget=1/2")
	assert.Equal(t, Usage{Wall: "1s", Budget: "1/2", Tokens: None(), Wait: Unreported, Run: Unreported}, old, "the older shape reads as a record with no token, no time and no cost")
	assert.Equal(t, "wall=1s budget=1/2 cost=none", old.String())

	u = ParseUsage("input=-5 output=abc requests=0 wait=abc run=-1s wait=1m long=no cost=both")
	assert.Equal(t, Unreported, u.Tokens.Input, "a negative count is unreported")
	assert.Equal(t, Unreported, u.Tokens.Output, "an unreadable count is unreported")
	assert.Equal(t, int64(0), u.Tokens.Requests)
	assert.Equal(t, int64(60), u.Wait, "the last readable wait wins")
	assert.Equal(t, Unreported, u.Run, "a negative duration is unreported")
	assert.False(t, u.Long)
	assert.Equal(t, CostNone, u.Present(), "cost= is derived, never read")
	assert.Nil(t, u.Extra)

	u = Usage{Model: "a b\tc", Wall: "1 s", Tokens: None(), Wait: Unreported, Run: Unreported}
	assert.Equal(t, "wall=1_s model=a_b_c cost=none", u.String(), "a value is kept one word")
	assert.Equal(t, int64(0), ParseUsage("wait=0s run=999ms").Run, "a duration under a second reads as zero seconds")

	for _, tc := range []struct {
		u    Usage
		want string
	}{
		{Usage{}, CostNone}, {Usage{Predicted: "1"}, CostPredicted}, {Usage{Actual: "1"}, CostActual}, {Usage{Predicted: "1", Actual: "1"}, CostBoth},
	} {
		assert.Equal(t, tc.want, tc.u.Present())
	}
}

// Priced: route "" is unpriced=no-route; a priced route copies its sheet and predicts; a
// route with no sheet prices nothing, and a harness actual of exactly zero beside reported
// tokens is dropped then, never kept as $0; a non-zero actual, or one beside unreported
// tokens, stays; the earlier pricing is cleared first.
func TestPricedNeverKeepsAZeroActualWhereNoSheetPrices(t *testing.T) {
	t.Parallel()
	base := NoUsage()
	base.Tokens.Input = 1_000_000
	base.Route, base.Prices, base.Predicted, base.Long, base.Unpriced = "old", "in:9", "9", true, "old-why"

	u := base.Priced("", Prices{Input: "1"})
	assert.Equal(t, Usage{Tokens: base.Tokens, Wait: Unreported, Run: Unreported, Unpriced: WhyNoRoute}, u)

	u = base.Priced("pro-a", Prices{Input: "0.5"})
	assert.Equal(t, Usage{Tokens: base.Tokens, Wait: Unreported, Run: Unreported, Route: "pro-a", Prices: "in:0.5,ro:false", Predicted: "0.5"}, u)

	withZero := base
	withZero.Actual, withZero.ActualBy = "0", ActualByHarness
	u = withZero.Priced("plan-a", Prices{})
	assert.Equal(t, Usage{Tokens: base.Tokens, Wait: Unreported, Run: Unreported, Route: "plan-a", Unpriced: WhyNoSheet}, u, "a zero actual with no sheet is no cost at all")
	u = withZero.Priced("pro-a", Prices{Input: "0.5"})
	assert.Equal(t, "0", u.Actual, "a priced route leaves the actual as it is")
	withZero.Actual = "0.00"
	assert.Equal(t, "", withZero.Priced("plan-a", Prices{}).Actual, "0.00 is an exact zero too")

	withCost := base
	withCost.Actual, withCost.ActualBy = "0.5", ActualByHarness
	u = withCost.Priced("plan-a", Prices{})
	assert.Equal(t, "0.5", u.Actual)
	assert.Equal(t, ActualByHarness, u.ActualBy)
	assert.Equal(t, WhyNoSheet, u.Unpriced)

	noTokens := NoUsage()
	noTokens.Actual, noTokens.ActualBy = "0", ActualByHarness
	u = noTokens.Priced("plan-a", Prices{})
	assert.Equal(t, "0", u.Actual, "with no tokens reported the zero actual stays: nothing says the run was priced")

	unreadable := base
	unreadable.Actual = "zero"
	assert.Equal(t, "zero", unreadable.Priced("plan-a", Prices{}).Actual, "an unreadable actual is not an exact zero")

	long := base
	long.Tokens.MaxPrompt = 2000
	u = long.Priced("pro-a", Prices{Input: "1", LongContext: 1000, InputLong: "2"})
	assert.True(t, u.Long)
	assert.Equal(t, "2", u.Predicted)
}

// Timed reads the wait from the dealt stamp to the taken stamp and the run from taken to
// end, each to the second; a missing or unreadable stamp, or an order that runs backwards,
// leaves that time unreported.
func TestTimedLeavesAMissingOrBackwardsTimeUnreported(t *testing.T) {
	t.Parallel()
	from, began := "2026-10-12T10:00:00Z", "2026-10-12T10:00:03Z"
	end := time.Date(2026, 10, 12, 10, 0, 55, 500_000_000, time.UTC)
	u := Usage{}.Timed(from, began, end)
	assert.Equal(t, int64(3), u.Wait)
	assert.Equal(t, int64(52), u.Run)
	u = Usage{}.Timed("", began, end)
	assert.Equal(t, Unreported, u.Wait)
	assert.Equal(t, int64(52), u.Run)
	u = Usage{}.Timed(from, "", end)
	assert.Equal(t, Unreported, u.Wait)
	assert.Equal(t, Unreported, u.Run)
	u = Usage{}.Timed("yesterday", began, end)
	assert.Equal(t, Unreported, u.Wait)
	u = Usage{}.Timed(began, from, end)
	assert.Equal(t, Unreported, u.Wait, "taken before dealt is no wait")
	u = Usage{}.Timed(from, began, time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC))
	assert.Equal(t, int64(3), u.Wait)
	assert.Equal(t, Unreported, u.Run, "an end before taken is no run")
	u = Usage{Wait: 7, Run: 7}.Timed(from, from, time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC))
	assert.Equal(t, int64(0), u.Wait, "the same stamp is a zero wait, and an earlier value is overwritten")
	assert.Equal(t, int64(0), u.Run)
}

// SpendWord and ParseSpend are inverse: the parts in order, one left out when unreported,
// a cost that is no decimal or a part holding a comma dropped, the model kept one word.
func TestSpendWordRoundTrip(t *testing.T) {
	t.Parallel()
	tk := Tokens{Input: 1, CacheRead: 2, CacheWrite: 3, Output: 4, Reasoning: 5, Requests: 6, MaxPrompt: 7}
	w := SpendWord(tk, "0.5", "p/m x")
	assert.Equal(t, "input:1,cache_read:2,cache_write:3,output:4,reasoning:5,requests:6,max_prompt:7,cost:0.5,model:p/m_x", w)
	u := ParseSpend(w)
	assert.Equal(t, tk, u.Tokens)
	assert.Equal(t, "0.5", u.Actual)
	assert.Equal(t, ActualByHarness, u.ActualBy)
	assert.Equal(t, "p/m_x", u.Model)
	assert.Equal(t, Unreported, u.Wait)

	assert.Equal(t, "", SpendWord(None(), "", ""))
	assert.Equal(t, "output:4,cost:0.5", SpendWord(Tokens{Input: Unreported, CacheRead: Unreported, CacheWrite: Unreported, Output: 4, Reasoning: Unreported, Requests: Unreported, MaxPrompt: Unreported}, "0.5", ""))
	assert.Equal(t, "input:0", SpendWord(Tokens{Input: 0, CacheRead: Unreported, CacheWrite: Unreported, Output: Unreported, Reasoning: Unreported, Requests: Unreported, MaxPrompt: Unreported}, "1,5", "a,b"), "a part holding a comma is left out")

	u = ParseSpend("input:10,cost:abc,model:,bare,output:,requests:-1,:x")
	assert.Equal(t, int64(10), u.Tokens.Input)
	assert.Equal(t, "", u.Actual, "a cost that is no decimal is no cost")
	assert.Equal(t, "", u.ActualBy)
	assert.Equal(t, "", u.Model)
	assert.Equal(t, Unreported, u.Tokens.Output)
	assert.Equal(t, Unreported, u.Tokens.Requests)
	assert.Equal(t, NoUsage(), ParseSpend(""))
}

// Words splits an actual_by list; "" is none.
func TestWords(t *testing.T) {
	t.Parallel()
	assert.Nil(t, Words(""))
	assert.Equal(t, []string{"harness"}, Words("harness"))
	assert.Equal(t, []string{"harness", "-"}, Words("harness+-"))
}
