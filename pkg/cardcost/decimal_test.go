package cardcost

import (
	"math/big"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A price is a decimal string end to end (decimal.go): read exactly, written in one
// spelling, never through a float.

func TestCanonicalIsTheOneSpellingOfADecimal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, in, want, err string
	}{
		{name: "not set", in: "", want: ""},
		{name: "a trailing zero", in: "0.30", want: "0.3"},
		{name: "leading zeros", in: "007", want: "7"},
		{name: "a point with zeros after it", in: "1.000", want: "1"},
		{name: "zero", in: "0.0", want: "0"},
		{name: "a tiny price", in: "0.0000125", want: "0.0000125"},
		{name: "more digits than a float64 holds", in: "0.10000000000000000000000000001", want: "0.10000000000000000000000000001"},
		{name: "a big one", in: "123456789012345678901234567890.5", want: "123456789012345678901234567890.5"},
		{name: "thirty digits after the point, the most", in: "0.000000000000000000000000000001", want: "0.000000000000000000000000000001"},
		{name: "thirty-one digits after the point", in: "0.0000000000000000000000000000001", err: "want at most 30 digits after the point"},
		{name: "thirty-one digits, trailing zeros too", in: "1.0000000000000000000000000000000", err: "want at most 30 digits after the point"},
		{name: "a sign", in: "-1", err: "want a non-negative decimal"},
		{name: "a plus", in: "+1", err: "want a non-negative decimal"},
		{name: "an exponent", in: "1e-6", err: "no sign or exponent"},
		{name: "a bare point", in: ".5", err: "want a non-negative decimal"},
		{name: "a comma", in: "1,5", err: "want a non-negative decimal"},
		{name: "a fraction", in: "1/3", err: "want a non-negative decimal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Canonical(tc.in)
			if tc.err != "" {
				assert.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestTextWritesATerminatingRationalExactly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		r    *big.Rat
		want string
	}{
		{name: "an integer", r: big.NewRat(42, 1), want: "42"},
		{name: "a millionth", r: big.NewRat(1, 1_000_000), want: "0.000001"},
		{name: "a power of two below one", r: big.NewRat(1, 1024), want: "0.0009765625"},
		{name: "tokens times a price over a million", r: new(big.Rat).Quo(new(big.Rat).Mul(big.NewRat(19541, 1), big.NewRat(27, 100)), big.NewRat(1_000_000, 1)), want: "0.00527607"},
		{name: "zero", r: new(big.Rat), want: "0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Text(tc.r))
		})
	}
}

// Sum is the caller's path for stored costs, which amount accepts of any length
// (decimal.go): every row asserts the exact decimal string comes back through Sum,
// so a stored cost is never cut or silently zeroed.
func TestExactSumPreservesStoredDecimalPrecision(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		vals []string
		want string
	}{
		{name: "a 101-place stored amount survives", vals: []string{"0." + strings.Repeat("0", 100) + "1"},
			want: "0." + strings.Repeat("0", 100) + "1"},
		{name: "a 150-place amount added to an ordinary one", vals: []string{"1.5", "0." + strings.Repeat("0", 149) + "7"},
			want: "1.5" + strings.Repeat("0", 148) + "7"},
		{name: "a carry across the decimal point", vals: []string{"0." + strings.Repeat("9", 101), "0." + strings.Repeat("0", 100) + "1"},
			want: "1"},
		{name: "leading and trailing zeros canonicalize", vals: []string{"007.500", "00.0", "0.500"}, want: "8"},
		{name: "trailing zeros on a small sum", vals: []string{"0.30", "0.2000"}, want: "0.5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := Sum(tc.vals...)
			assert.True(t, ok, "Sum(%q) accepts a stored decimal of any length", tc.vals)
			assert.Equal(t, tc.want, got)
		})
	}
	t.Run("typed Decimal still refuses more than MaxFraction digits", func(t *testing.T) {
		t.Parallel()
		_, err := Decimal("0." + strings.Repeat("0", 30) + "1")
		assert.ErrorContains(t, err, "want at most 30 digits after the point")
	})
	t.Run("a nonterminating rational stays bounded", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "0."+strings.Repeat("3", 100), Text(new(big.Rat).SetFrac(big.NewInt(1), big.NewInt(3))))
	})
}
