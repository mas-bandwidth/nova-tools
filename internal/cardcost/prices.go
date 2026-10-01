package cardcost

import (
	"math/big"
	"strconv"
	"strings"
)

// Prices is one route's price sheet. Every amount is a canonical decimal string,
// "" when not set.
type Prices struct {
	Input             string `json:"input,omitempty"`
	CacheRead         string `json:"cache_read,omitempty"`
	CacheWrite        string `json:"cache_write,omitempty"`
	Output            string `json:"output,omitempty"`
	ReasoningAsOutput bool   `json:"reasoning_as_output"`
	LongContext       int64  `json:"long_context,omitempty"`
	InputLong         string `json:"input_long,omitempty"`
	OutputLong        string `json:"output_long,omitempty"`
	Request           string `json:"request,omitempty"`
	Billing           string `json:"billing,omitempty"`
	GatewayPercent    string `json:"gateway_percent,omitempty"`
	Source            string `json:"source,omitempty"`
	AsOf              string `json:"as_of,omitempty"`
}

// PricesOf is the price sheet of a route's fields, as a config row or the
// route's hash holds them: a field missing is not set, and reasoning is billed as
// output unless the field says false.
func PricesOf(f map[string]string) Prices {
	n, _ := strconv.ParseInt(f[FieldLongContext], 10, 64)
	ro, err := strconv.ParseBool(f[FieldReasoningAsOutput])
	if err != nil {
		ro = true
	}
	return Prices{Input: f[FieldInput], CacheRead: f[FieldCacheRead], CacheWrite: f[FieldCacheWrite], Output: f[FieldOutput],
		ReasoningAsOutput: ro, LongContext: n, InputLong: f[FieldInputLong], OutputLong: f[FieldOutputLong], Request: f[FieldRequest],
		Billing: f[FieldBilling], GatewayPercent: f[FieldGateway], Source: f[FieldSource], AsOf: f[FieldAsOf]}
}

// Priced says the sheet holds a price: a route with none has no predicted cost.
func (p Prices) Priced() bool {
	return p.Input != "" || p.CacheRead != "" || p.CacheWrite != "" || p.Output != "" || p.InputLong != "" || p.OutputLong != "" || p.Request != ""
}

// copyKeys are the short names of the sheet's copy on a card, in its order.
var copyKeys = []string{"in", "cr", "cw", "out", "ro", "long", "inl", "outl", "req", "bill", "gw", "asof"}

// Copy is the sheet as a consumer card keeps it beside its predicted cost, so a
// later change of the route's prices does not rewrite what the card cost: one word,
// in:<p>,cr:<p>,cw:<p>,out:<p>,ro:<bool>,long:<n>,inl:<p>,outl:<p>,req:<usd>,
// bill:<kind>,gw:<pct>,asof:<date>, each part left out when not set. The source is
// free text and stays on the route's history (nova-config route history).
func (p Prices) Copy() string {
	vals := map[string]string{"in": p.Input, "cr": p.CacheRead, "cw": p.CacheWrite, "out": p.Output, "ro": strconv.FormatBool(p.ReasoningAsOutput),
		"inl": p.InputLong, "outl": p.OutputLong, "req": p.Request, "bill": p.Billing, "gw": p.GatewayPercent, "asof": p.AsOf}
	if p.LongContext > 0 {
		vals["long"] = strconv.FormatInt(p.LongContext, 10)
	}
	var parts []string
	for _, k := range copyKeys {
		if v := vals[k]; v != "" && !strings.ContainsAny(v, ", \t\n") {
			parts = append(parts, k+":"+v)
		}
	}
	return strings.Join(parts, ",")
}

// ParseCopy is the sheet a card's copy holds (Copy); a part it does not know is skipped.
func ParseCopy(s string) Prices {
	f := map[string]string{}
	keys := map[string]string{"in": FieldInput, "cr": FieldCacheRead, "cw": FieldCacheWrite, "out": FieldOutput, "ro": FieldReasoningAsOutput,
		"long": FieldLongContext, "inl": FieldInputLong, "outl": FieldOutputLong, "req": FieldRequest, "bill": FieldBilling, "gw": FieldGateway, "asof": FieldAsOf}
	for _, part := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(part, ":")
		if name, known := keys[k]; ok && known {
			f[name] = v
		}
	}
	return PricesOf(f)
}

// Sum is the exact sum of decimal strings, "" left out; ok is false when one is
// not a decimal.
func Sum(vals ...string) (sum string, ok bool) {
	total := new(big.Rat)
	for _, v := range vals {
		if v == "" {
			continue
		}
		r, err := Decimal(v)
		if err != nil {
			return "", false
		}
		total.Add(total, r)
	}
	return Text(total), true
}
