package cardcost

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The total a run's tokens add up to (predict.go): the classes reported, and a
// class that is zero or unreported left out of it.

// TestPredictCoverTotal covers Tokens.Total (predict.go:39): the sum of the five
// token classes the harness reported, and the zero, negative and unreported
// classes left out of it. Total has no refusal branch; its nearest refusal is
// the run of which nothing was reported, which totals zero, never a negative.
func TestPredictCoverTotal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		t    Tokens
		want int64
	}{
		{
			name: "every class reported sums",
			t:    run(11, 20, 5, 5, 70),
			want: 111,
		},
		{
			name: "zero, negative and unreported classes are left out",
			t:    Tokens{Input: 7, CacheRead: 0, CacheWrite: Unreported, Output: -3, Reasoning: 2},
			want: 9,
		},
		{
			name: "a run of which nothing was reported totals zero",
			t:    None(),
			want: 0,
		},
		{
			name: "the request count and prompt size are not token classes",
			t:    Tokens{Output: 3, Requests: 100, MaxPrompt: 200},
			want: 3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.t.Total())
		})
	}
}
