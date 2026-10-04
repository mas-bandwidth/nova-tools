package swarm

// USAGE COVER: the two readers of a usage row's numbers (usage.go).
//
// The named tests are TestUsageCover*, so `go test -run TestUsageCover` selects exactly
// this file. Each covers one listed function's main path and the one reading it refuses:
// Int, the number reader for which a dash is an absence and not a number (usage.go:38), and
// BudgetWord, the four spellings of ceiling over observed (usage.go:85). Both are pure over
// their arguments, so no seam, store or subprocess is reached; nothing here sleeps,
// dials or forks.

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUsageCoverRowInt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		row   UsageRow
		cell  string
		want  int
		isNum bool
	}{
		{
			name:  "a measured cell reads as the number it measured",
			row:   UsageRow{"tokens_in": "12000"},
			cell:  "tokens_in",
			want:  12000,
			isNum: true,
		},
		{
			name:  "spaces around the digits are trimmed before the read",
			row:   UsageRow{"rc": " 0 "},
			cell:  "rc",
			want:  0,
			isNum: true,
		},
		{
			name:  "a measured zero is a number, not an absence",
			row:   UsageRow{"reasoning": "0"},
			cell:  "reasoning",
			want:  0,
			isNum: true,
		},
		{
			name:  "the dash is an absence and never reads as a number",
			row:   UsageRow{"cache_read": Dash},
			cell:  "cache_read",
			want:  0,
			isNum: false,
		},
		{
			name:  "a column the row does not carry reads as nothing",
			row:   UsageRow{"job": "cover-x"},
			cell:  "tokens_out",
			want:  0,
			isNum: false,
		},
		{
			name:  "a word where a number belongs is refused, not guessed",
			row:   UsageRow{"usd": "many"},
			cell:  "usd",
			want:  0,
			isNum: false,
		},
		{
			name:  "a non-integer number is refused the integer read",
			row:   UsageRow{"usd": "0.0010"},
			cell:  "usd",
			want:  0,
			isNum: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := tc.row.Int(tc.cell)
			assert.Equal(t, tc.isNum, ok, "is a number at all")
			assert.Equal(t, tc.want, got, "the read number, zero on any refusal")
		})
	}
}

func TestUsageCoverBudgetWord(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		unmetered bool
		tokens    int
		spent     int
		observed  bool
		partial   bool
		want      string
	}{
		{
			name:      "unmetered: the caller said this provider has no live accounting",
			unmetered: true,
			tokens:    100,
			want:      "unmetered",
		},
		{
			name:     "nothing observed is the dash over the ceiling, never 0/<n>",
			tokens:   100,
			observed: false,
			want:     Dash + "/100",
		},
		{
			name:     "a partial observation shows the plus and can never show it stayed under",
			tokens:   100,
			spent:    40,
			observed: true,
			partial:  true,
			want:     "40+/100",
		},
		{
			name:     "a whole observation is the sum over the ceiling",
			tokens:   100,
			spent:    40,
			observed: true,
			want:     "40/100",
		},
		{
			name:     "a whole observation measured at zero reads 0, not the dash",
			tokens:   100,
			spent:    0,
			observed: true,
			want:     "0/100",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := BudgetWord(tc.unmetered, tc.tokens, tc.spent, tc.observed, tc.partial)
			assert.Equal(t, tc.want, got, "the spelling the document names")
		})
	}
}
