package swarm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The card usage fold keeps what a card's cost record reads (usagecard.go,
// foldCardMessages): the harness's cost as the exact decimal of what the store
// summed, the requests and the largest prompt, beside the row's columns.

func TestTheCardFoldKeepsTheHarnessCostRequestsAndLargestPrompt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		rows [][]string
		want map[string]string
	}{
		{
			name: "one model: the row's usd cut to four places, the cost whole",
			rows: [][]string{{"opencode", "deepseek-v4-pro", "19541", "692", "0", "36336", "70", "0.04219614", "6", "9861"}},
			want: map[string]string{"tokens_in": "19541", "cache_read": "36336", "usd": "0.0422", "cost": "0.04219614", "requests": "6", "max_prompt": "9861"},
		},
		{
			name: "two models: the costs summed exactly, the requests added, the largest prompt kept",
			rows: [][]string{
				{"opencode", "a", "1", "1", "0", "0", "0", "0.1", "2", "500"},
				{"opencode", "b", "1", "1", "0", "0", "0", "0.2", "3", "900"},
			},
			want: map[string]string{"usd": "0.3000", "cost": "0.3", "requests": "5", "max_prompt": "900"},
		},
		{
			name: "a cost the store wrote with an exponent",
			rows: [][]string{{"p", "m", "10", "1", "0", "0", "0", "1.5e-05", "1", "10"}},
			want: map[string]string{"cost": "0.000015", "usd": "0.0000"},
		},
		{
			name: "no cost reported: no cost, never 0",
			rows: [][]string{{"p", "m", "10", "1", "0", "0", "0", "", "1", "10"}},
			want: map[string]string{"usd": Dash, "cost": ""},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u, _ := foldCardMessages(tc.rows)
			for k, v := range tc.want {
				assert.Equal(t, v, u.Values[k], k)
			}
		})
	}
}
