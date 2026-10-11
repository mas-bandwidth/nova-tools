package cardcost

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The total a producer card keeps (usage.go) reads back as the line it writes:
// Total.String and ParseTotal, each its main path and its refusal to take a bad
// value.

// TestUsageCoverTotalString covers Total.String (usage.go:336): a full total's
// line, and the refusal of a count or time that is unreported (negative) is left
// out of the line.
func TestUsageCoverTotalString(t *testing.T) {
	t.Parallel()
	tk := run(11, 20, 5, 5, 70)
	tk.Requests, tk.MaxPrompt = 7, 50
	cases := []struct {
		name  string
		total Total
		want  string
	}{
		{
			name: "a full total",
			total: Total{
				Records:   3,
				Tokens:    tk,
				Wait:      3,
				Run:       10,
				Predicted: "0.1",
				PredOf:    2,
				Actual:    "0.2",
				ActualOf:  1,
				ActualBy:  "harness",
				Charged:   "0.3",
				ChargedOf: 3,
			},
			want: "records=3 input=11 cache_read=20 cache_write=5 output=5 reasoning=70 requests=7 max_prompt=50 wait_s=3 run_s=10 " +
				"predicted_usd=0.1 actual_usd=0.2 actual_by=harness charged_usd=0.3 predicted_of=2 actual_of=1 charged_of=3",
		},
		{
			name: "unreported counts and times are left out",
			total: Total{
				Records: 1,
				Tokens:  None(),
				Wait:    Unreported,
				Run:     Unreported,
			},
			want: "records=1 predicted_of=0 actual_of=0 charged_of=0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.total.String())
			// a written total reads back as itself
			assert.Equal(t, tc.want, ParseTotal(tc.total.String()).String())
			// a line that is all refusals stays valid
			require.NotContains(t, tc.total.String(), "=-")
		})
	}
}

// TestUsageCoverParseTotal covers ParseTotal (usage.go:356): a full total line,
// and the refusal of a count that is negative or unreadable (the field keeps its
// unreported default).
func TestUsageCoverParseTotal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		line     string
		wantLine string // ParseTotal(line).String() ("==" means the round trip)
		check    func(t *testing.T, tot Total)
	}{
		{
			name: "a full total line",
			line: "records=3 input=11 cache_read=20 cache_write=5 output=5 reasoning=70 requests=7 max_prompt=50 wait_s=3 run_s=10 " +
				"predicted_usd=0.1 actual_usd=0.2 actual_by=harness charged_usd=0.3 predicted_of=2 actual_of=1 charged_of=3",
			wantLine: "==",
			check: func(t *testing.T, tot Total) {
				assert.Equal(t, 3, tot.Records)
				assert.Equal(t, int64(11), tot.Tokens.Input)
				assert.Equal(t, int64(20), tot.Tokens.CacheRead)
				assert.Equal(t, int64(5), tot.Tokens.CacheWrite)
				assert.Equal(t, int64(5), tot.Tokens.Output)
				assert.Equal(t, int64(70), tot.Tokens.Reasoning)
				assert.Equal(t, int64(7), tot.Tokens.Requests)
				assert.Equal(t, int64(50), tot.Tokens.MaxPrompt)
				assert.Equal(t, int64(3), tot.Wait)
				assert.Equal(t, int64(10), tot.Run)
				assert.Equal(t, "0.1", tot.Predicted)
				assert.Equal(t, 2, tot.PredOf)
				assert.Equal(t, "0.2", tot.Actual)
				assert.Equal(t, 1, tot.ActualOf)
				assert.Equal(t, "harness", tot.ActualBy)
				assert.Equal(t, "0.3", tot.Charged)
				assert.Equal(t, 3, tot.ChargedOf)
			},
		},
		{
			name: "a negative or unreadable count is not taken",
			line: "records=2 input=-5 output=abc wait_s=-1 run_s=x predicted_of=1 actual_of=0 charged_of=0 unknown=ignored",
			check: func(t *testing.T, tot Total) {
				assert.Equal(t, 2, tot.Records)
				assert.Equal(t, Unreported, tot.Tokens.Input, "a negative count is refused")
				assert.Equal(t, Unreported, tot.Tokens.Output, "an unreadable count is refused")
				assert.Equal(t, Unreported, tot.Wait, "a negative time is refused")
				assert.Equal(t, Unreported, tot.Run, "an unreadable time is refused")
				assert.Equal(t, 1, tot.PredOf)
				assert.Equal(t, "", tot.Predicted, "no predicted value was given")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tot := ParseTotal(tc.line)
			tc.check(t, tot)
			// the main path round-trips; a line of refusals re-strings to its kept values
			got := tot.String()
			if tc.wantLine == "==" {
				assert.Equal(t, tc.line, got, "ParseTotal reads back its own line")
			} else {
				assert.NotContains(t, got, "=-", "no refused value leaks into the line")
			}
		})
	}
}
