package cardcost

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDecimalCoverCents covers Cents (decimal.go:160): a rational amount shown as
// dollars and cents, rounded up to the next cent, and its refusal to add a cent to
// an amount that is already exact.
func TestDecimalCoverCents(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		usd  *big.Rat
		want string
	}{
		{name: "a fraction of a cent rounds up", usd: big.NewRat(12345, 10000), want: "$1.24"},
		{name: "a fraction past twenty dollars rounds up", usd: big.NewRat(202111, 10000), want: "$20.22"},
		{name: "a whole number of cents is not rounded up", usd: big.NewRat(100, 100), want: "$1.00"},
		{name: "zero", usd: new(big.Rat), want: "$0.00"},
		{name: "a hundredth of a cent rounds up to one cent", usd: big.NewRat(1, 10000), want: "$0.01"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Cents(tc.usd))
		})
	}
}
