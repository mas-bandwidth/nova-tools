package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFriendTotalsPreserveTokenAndChargedSemantics(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		lines   []string
		tokens  int64
		charged string
	}{
		{name: "no cards"},
		{name: "missing usage", lines: []string{"", "wall=2s budget=old"}},
		{name: "all counted classes", lines: []string{"input=1 output=2 cache_read=3 cache_write=4 reasoning=99 requests=5"}, tokens: 10},
		{name: "malformed counts preserve prior values", lines: []string{"input=7 input=-1 input=bad output=1.5 cache_read=0 cache_write=3"}, tokens: 10},
		{name: "repeated words retain last valid count", lines: []string{"input=2 input=3 output=4 output=bad actual_usd=9 actual_usd=0.125"}, tokens: 7, charged: "0.125"},
		{name: "invalid actual prediction", lines: []string{"input=5 actual_usd=bad predicted_usd=0.25"}, tokens: 5, charged: "0.25"},
		{name: "actual zero", lines: []string{"input=0 actual_usd=0 predicted_usd=1.25"}, charged: "0"},
		{name: "invalid costs", lines: []string{"input=5 actual_usd=-1 predicted_usd=1e-2"}, tokens: 5},
		{name: "exact sub cents", lines: []string{"actual_usd=0.0001", "predicted_usd=0.0002"}, charged: "0.0003"},
		{name: "long stored precision", lines: []string{"actual_usd=0." + strings.Repeat("0", 60) + "1", "predicted_usd=0." + strings.Repeat("0", 60) + "1"}, charged: "0." + strings.Repeat("0", 60) + "2"},
		{name: "per card overflow order retained", lines: []string{"input=9223372036854775807 output=1", "input=1"}, tokens: -9223372036854775807},
		{name: "mixed reports", lines: []string{"input=3 output=5 actual_usd=0.1", "cache_read=7 actual_usd=bad predicted_usd=0.2", "cache_write=9 actual_usd=0 predicted_usd=9", "reasoning=11"}, tokens: 24, charged: "0.3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cards := make([]*Card, 0, len(tc.lines))
			for _, line := range tc.lines {
				cards = append(cards, &Card{Fields: map[string]string{FieldUsage: line}})
			}
			tokens, charged := FriendTotalsFromCards(cards)
			assert.Equal(t, tc.tokens, tokens)
			assert.Equal(t, tc.charged, charged)
			assert.Equal(t, FriendTokensFromCards(cards), tokens)
			assert.Equal(t, FriendChargedFromCards(cards), charged)
		})
	}
}
