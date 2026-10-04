package tokens

// The cover card for claude_session.go: the two functions the unit tier's table showed at
// 0.0% -- Flag and ParseWeights -- each with its main path and a refusal, pure data with no
// real time, no network, no subprocess and no store.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClaudeSessionCoverFlagRendersDefaultWeights: Flag renders the four weights as the
// in,cw,cr,out comma-separated numbers the session verb's --weights flag takes, so the
// default is DefaultWeights printed and never a second copy of it.
func TestClaudeSessionCoverFlagRendersDefaultWeights(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		w    Weights
		want string
	}{
		{
			name: "DefaultWeights prints as 1,1.25,0.1,5",
			w:    DefaultWeights,
			want: "1,1.25,0.1,5",
		},
		{
			name: "integer weights print without a decimal point",
			w:    Weights{Input: 2, CacheWrite: 3, CacheRead: 4, Output: 5},
			want: "2,3,4,5",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.w.Flag()
			assert.Equalf(t, tc.want, got, "Flag(%v) = %q, want %q: the flag's default is DefaultWeights printed", tc.w, got, tc.want)
		})
	}
}

// TestClaudeSessionCoverParseWeightsRoundTripsDefaultWeights: ParseWeights reads back the four
// numbers Flag prints, so the default carries itself without a second copy of the ratios.
func TestClaudeSessionCoverParseWeightsRoundTripsDefaultWeights(t *testing.T) {
	t.Parallel()

	got, err := ParseWeights(DefaultWeights.Flag())
	require.NoErrorf(t, err, "ParseWeights(%q) returned an error for its own output", DefaultWeights.Flag())
	assert.Equalf(t, DefaultWeights, got, "ParseWeights(%q) = %v, want %v: Flag and ParseWeights are inverses", DefaultWeights.Flag(), got, DefaultWeights)
}

// TestClaudeSessionCoverParseWeightsRefusesBadInput: a value that is not four finite numbers
// is a refusal saying what the flag WANTS, never a guess -- too few parts, too many parts, a
// non-number, a NaN, and a non-finite infinity each named.
func TestClaudeSessionCoverParseWeightsRefusesBadInput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   string
	}{
		{name: "too few parts", in: "1,1.25,0.1"},
		{name: "too many parts", in: "1,1.25,0.1,5,1"},
		{name: "a non-number", in: "1,1.25,0.1,not-a-number"},
		{name: "a NaN", in: "1,1.25,0.1,NaN"},
		{name: "a positive infinity", in: "1,1.25,0.1,+Inf"},
		{name: "a negative infinity", in: "1,1.25,0.1,-Inf"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseWeights(tc.in)
			require.Errorf(t, err, "ParseWeights(%q) = (%v, nil), want a refusal saying what the flag WANTS", tc.in, got)
			assert.Equalf(t, Weights{}, got, "ParseWeights(%q) returned %v, want zero Weights on a refusal", tc.in, got)
			assert.Truef(t, strings.Contains(err.Error(), "wants four comma-separated"), "ParseWeights(%q) error=%q, want it to name what the flag wants", tc.in, err.Error())
		})
	}
}
