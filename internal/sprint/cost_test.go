package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A read card asked again of its reader after a return keeps the one card id (a
// return is not a read), so each run's record is numbered (FieldReadTake) and the
// verdict run's is the card's usage: every run counts in the producer's total.
func TestEveryRunOfAReadCardCountsInTheTotal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		fields map[string]string
		ends   []string
		input  int64
	}{
		{name: "two returned runs and the verdict run", fields: map[string]string{
			FieldReadTake + "1": "input=10 actual_usd=0.1 actual_by=harness cost=actual",
			FieldReadTake + "2": "input=20 actual_usd=0.2 actual_by=harness cost=actual",
			FieldUsage:          "input=30 actual_usd=0.3 actual_by=harness cost=actual",
			"read":              "2026-10-01T12:00:00Z", "verdict": "ok"},
			ends: []string{"returned", "returned", "ok"}, input: 60},
		{name: "a returned run, asked again and not yet read", fields: map[string]string{
			FieldReadTake + "1": "input=10 cost=none"},
			ends: []string{"returned"}, input: 10},
		{name: "a gap ends the runs: the numbering is dense", fields: map[string]string{
			FieldReadTake + "1": "input=10 cost=none", FieldReadTake + "3": "input=99 cost=none"},
			ends: []string{"returned"}, input: 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := map[string]string{"reader": "reader-a", "attempt": "1"}
			for k, v := range tc.fields {
				f[k] = v
			}
			v := CardCost(nil, []*Card{{ID: "s1-1.r1.reader-a", Fields: f}})
			require.Len(t, v.Consumers, len(tc.ends))
			for i, end := range tc.ends {
				assert.Equal(t, end, v.Consumers[i].End)
			}
			assert.Equal(t, tc.input, v.Total.Tokens.Input)
		})
	}
}

func TestNextTakeIsOnePastTheLastRecord(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 1, nextTake(&Card{Fields: map[string]string{}}, FieldReadTake))
	assert.Equal(t, 3, nextTake(&Card{Fields: map[string]string{FieldReadTake + "1": "x", FieldReadTake + "2": "y"}}, FieldReadTake))
}
