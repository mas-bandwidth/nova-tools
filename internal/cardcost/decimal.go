// Package cardcost is what a card cost: a route's price sheet as nova-config holds it
// (the route kind's price fields, docs/SPEC-CONFIG.md, "route"), in exact decimal
// arithmetic. Every amount is a decimal string end to end, never a float: a price is
// typed as a decimal, kept as one in Postgres and Redis, and read back as a rational.
//
// Libraries considered: math/big's Rat (the standard library) does exact decimal
// arithmetic on strings of any length; no adopted module does decimals, and
// internal/tokens keeps micro-dollars, which would round a price per token.
package cardcost

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

// The route kind's price fields (internal/config/kind.go), the names add and set
// take as flags, the columns of config.routes and the keys apply writes into the
// hash route:<name>, which the sprint reads with the routes. Prices are USD per
// million tokens unless the name says otherwise.
const (
	FieldInput             = "price_input"         // uncached input
	FieldCacheRead         = "price_cache_read"    // cached input read
	FieldCacheWrite        = "price_cache_write"   // cache write
	FieldOutput            = "price_output"        // output
	FieldReasoningAsOutput = "reasoning_as_output" // bool: reasoning tokens billed at the output price
	FieldLongContext       = "long_context"        // tokens: a request whose prompt is above it is priced long
	FieldInputLong         = "price_input_long"    // input above the threshold
	FieldOutputLong        = "price_output_long"   // output above the threshold
	FieldRequest           = "price_request"       // USD per request
	FieldBilling           = "billing"             // metered or plan
	FieldGateway           = "gateway_percent"     // percent added on top by a gateway
	FieldSource            = "price_source"        // free text: where the prices were read
	FieldAsOf              = "price_as_of"         // the date they were read, YYYY-MM-DD
)

// The billing kinds: metered is paid per token; plan is paid by subscription, so
// the predicted cost is the metered price of the same tokens, not a charge.
const (
	BillingMetered = "metered"
	BillingPlan    = "plan"
)

// Billings are the billing kinds, the route's enum.
var Billings = []string{BillingMetered, BillingPlan}

// decimalPattern is a price as add and set take it: digits, and a fraction after
// one point; no sign, no exponent, no thousands separator.
var decimalPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// Decimal reads a non-negative decimal ("0.30", "15", "0.0000125") exactly.
func Decimal(s string) (*big.Rat, error) {
	if !decimalPattern.MatchString(s) {
		return nil, fmt.Errorf("%q: want a non-negative decimal like 0.30 (digits, one point, no sign or exponent)", s)
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("%q: want a non-negative decimal like 0.30", s)
	}
	return r, nil
}

// Canonical is a decimal's one spelling: no leading zero before the point but
// one, no trailing zero after it ("0.30" is "0.3", "007" is "7", "1.0" is "1");
// "" stays "" (not set).
func Canonical(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	r, err := Decimal(s)
	if err != nil {
		return "", err
	}
	return Text(r), nil
}

// maxDigits bounds the digits after the point Text looks for: a sum of decimals
// times integers over powers of ten terminates far inside it.
const maxDigits = 60

// Text is a rational whose decimal terminates, written exactly, with no trailing
// zero after the point (a value that does not terminate within maxDigits is cut
// there; no amount this package makes is one).
func Text(r *big.Rat) string {
	ten := big.NewInt(10)
	scaled := new(big.Rat).Set(r)
	digits := 0
	for !scaled.IsInt() && digits < maxDigits {
		scaled.Mul(scaled, new(big.Rat).SetInt(ten))
		digits++
	}
	s := r.FloatString(digits)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

