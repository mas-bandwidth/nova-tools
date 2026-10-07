package cardcost

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The usage record a consumer card keeps (usage.go): one line that reads back as what
// it says, an older line read as a record with nothing in it, the time from two
// stamps, the prediction with its sheet, native's spend word, and the totals.

func TestAUsageLineReadsBackAsItsRecord(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, line, again string // again: the line String writes ("" is line itself)
		check             func(t *testing.T, u Usage)
	}{
		{
			name: "the older shape: wall and budget only",
			line: "wall=49.49s budget=20088/400000", again: "wall=49.49s budget=20088/400000 cost=none",
			check: func(t *testing.T, u Usage) {
				assert.Equal(t, "49.49s", u.Wall)
				assert.False(t, u.Tokens.Reported())
				assert.Equal(t, Unreported, u.Wait)
				assert.Equal(t, CostNone, u.Present())
			},
		},
		{
			name: "a whole record",
			line: "wall=49.49s budget=20088/400000 input=19541 cache_read=36336 cache_write=0 output=692 reasoning=70 requests=6 max_prompt=9861 " +
				"model=opencode/deepseek-v4-pro actual_usd=0.04219614 actual_by=harness wait=3s run=52s price_route=pro-a prices=in:0.27,ro:true " +
				"predicted_usd=0.00527607 cost=both",
			check: func(t *testing.T, u Usage) {
				assert.Equal(t, int64(0), u.Tokens.CacheWrite, "a reported zero is a zero")
				assert.Equal(t, int64(6), u.Tokens.Requests)
				assert.Equal(t, int64(52), u.Run)
				assert.Equal(t, CostBoth, u.Present())
			},
		},
		{
			name: "unpriced, long, and a word it does not know kept",
			line: "input=5 long=yes unpriced=no-route custom=1 cost=none", again: "input=5 long=yes unpriced=no-route custom=1 cost=none",
			check: func(t *testing.T, u Usage) {
				assert.True(t, u.Long)
				assert.Equal(t, WhyNoRoute, u.Unpriced)
				assert.Equal(t, []string{"custom=1"}, u.Extra)
			},
		},
		{
			name: "a negative or unreadable count is unreported",
			line: "input=-4 output=x wait=-3s", again: "cost=none",
			check: func(t *testing.T, u Usage) {
				assert.Equal(t, Unreported, u.Tokens.Input)
				assert.Equal(t, Unreported, u.Tokens.Output)
				assert.Equal(t, Unreported, u.Wait)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u := ParseUsage(tc.line)
			tc.check(t, u)
			want := tc.again
			if want == "" {
				want = tc.line
			}
			assert.Equal(t, want, u.String())
			assert.Equal(t, u.String(), ParseUsage(u.String()).String(), "a written record reads back as itself")
		})
	}
}

func TestPricedCopiesTheSheetAndNeverGuesses(t *testing.T) {
	t.Parallel()
	u := ParseUsage("input=1000000 output=1000000 actual_usd=1.5 actual_by=harness")
	priced := u.Priced("pro-a", Prices{Input: "0.27", Output: "1.1", ReasoningAsOutput: true})
	assert.Equal(t, "1.37", priced.Predicted)
	assert.Equal(t, "in:0.27,out:1.1,ro:true", priced.Prices)
	assert.Equal(t, "pro-a", priced.Route)
	assert.Equal(t, "1.5", priced.Actual, "the harness's cost is kept beside the prediction, never replaced")
	assert.Equal(t, CostBoth, priced.Present())

	none := u.Priced("flash-a", Prices{ReasoningAsOutput: true})
	assert.Equal(t, "", none.Predicted, "a route with no price gives no prediction, never 0")
	assert.Equal(t, WhyNoSheet, none.Unpriced)
	assert.Equal(t, "", none.Prices)
	assert.Equal(t, CostActual, none.Present())

	nowhere := priced.Priced("", Prices{})
	assert.Equal(t, WhyNoRoute, nowhere.Unpriced)
	assert.Equal(t, "", nowhere.Predicted, "pricing again clears the earlier prediction")
	assert.Equal(t, "", nowhere.Route)
}

func TestTimedMeasuresWaitingAndRunning(t *testing.T) {
	t.Parallel()
	end := time.Date(2026, 10, 1, 12, 1, 0, 0, time.UTC)
	cases := []struct {
		name, from, began string
		wait, run         int64
	}{
		{name: "both stamps", from: "2026-10-01T12:00:00Z", began: "2026-10-01T12:00:10Z", wait: 10, run: 50},
		{name: "no deal stamp", began: "2026-10-01T12:00:10Z", wait: Unreported, run: 50},
		{name: "no take stamp", from: "2026-10-01T12:00:00Z", wait: Unreported, run: Unreported},
		{name: "taken before dealt", from: "2026-10-01T12:00:20Z", began: "2026-10-01T12:00:10Z", wait: Unreported, run: 50},
		{name: "ended before taken", from: "2026-10-01T12:00:00Z", began: "2026-10-01T12:02:00Z", wait: 120, run: Unreported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u := NoUsage().Timed(tc.from, tc.began, end)
			assert.Equal(t, tc.wait, u.Wait)
			assert.Equal(t, tc.run, u.Run)
		})
	}
}

func TestTheSpendWordCarriesTheJobsTokensCostAndModel(t *testing.T) {
	t.Parallel()
	tk := run(19541, 36336, 0, 692, 70)
	tk.Requests, tk.MaxPrompt = 6, 9861
	w := SpendWord(tk, "0.04219614", "opencode/deepseek-v4-pro")
	assert.Equal(t, "input:19541,cache_read:36336,cache_write:0,output:692,reasoning:70,requests:6,max_prompt:9861,cost:0.04219614,model:opencode/deepseek-v4-pro", w)
	u := ParseSpend(w)
	assert.Equal(t, tk, u.Tokens)
	assert.Equal(t, "0.04219614", u.Actual)
	assert.Equal(t, ActualByHarness, u.ActualBy)
	assert.Equal(t, "opencode/deepseek-v4-pro", u.Model)

	partial := ParseSpend(SpendWord(run(10, Unreported, Unreported, 5, Unreported), "", ""))
	assert.Equal(t, Unreported, partial.Tokens.CacheRead, "a class the harness did not report stays unreported")
	assert.Equal(t, "", partial.Actual, "no cost reported is no cost, never 0")
	assert.Equal(t, "", ParseSpend("cost:1e-3").Actual, "a cost that is not a decimal is not taken")
	assert.Equal(t, "", SpendWord(None(), "", ""))
}

// TestInvalidActualDoesNotSuppressValidPrediction pins Add's rule (usage.go,
// "a cost is never guessed"; Total's exact-cost contract): only a valid
// non-negative decimal amount is summed, counted and named as a reporter. A
// rejected actual must not stand in for, or contaminate, a valid prediction or a
// valid total already accumulated, and a rejected actual must not hide the valid
// prediction behind it.
func TestInvalidActualDoesNotSuppressValidPrediction(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		lines []string
		want  Total
	}{
		{
			name:  "a malformed actual does not suppress the valid prediction",
			lines: []string{"actual_usd=not-decimal actual_by=harness predicted_usd=0.25"},
			want:  Total{Predicted: "0.25", PredOf: 1, Charged: "0.25", ChargedOf: 1},
		},
		{
			name:  "a valid actual overrides the prediction",
			lines: []string{"actual_usd=0.5 actual_by=harness predicted_usd=0.25"},
			want:  Total{Predicted: "0.25", PredOf: 1, Actual: "0.5", ActualOf: 1, ActualBy: "harness", Charged: "0.5", ChargedOf: 1},
		},
		{
			name:  "zero is a valid actual and overrides the prediction",
			lines: []string{"actual_usd=0 actual_by=harness predicted_usd=0.25"},
			want:  Total{Predicted: "0.25", PredOf: 1, Actual: "0", ActualOf: 1, ActualBy: "harness", Charged: "0", ChargedOf: 1},
		},
		{
			name:  "a malformed prediction alone counts nothing",
			lines: []string{"predicted_usd=not-decimal actual_by=harness"},
			want:  Total{},
		},
		{
			name:  "both amounts malformed count nothing and name no reporter",
			lines: []string{"actual_usd=nope actual_by=harness predicted_usd=also-bad"},
			want:  Total{},
		},
		{
			name: "a rejected actual between valid records contaminates neither sum nor reporter",
			lines: []string{
				"actual_usd=0.1 actual_by=harness",
				"actual_usd=bad actual_by=other",
				"predicted_usd=0.2",
			},
			want: Total{Predicted: "0.2", PredOf: 1, Actual: "0.1", ActualOf: 1, ActualBy: "harness", Charged: "0.3", ChargedOf: 2},
		},
		{
			name: "a rejected actual leaves an already-summed valid actual alone",
			lines: []string{
				"actual_usd=0.5 actual_by=harness",
				"actual_usd=oops actual_by=other",
			},
			want: Total{Actual: "0.5", ActualOf: 1, ActualBy: "harness", Charged: "0.5", ChargedOf: 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tot := NoTotal()
			for _, line := range tc.lines {
				tot = tot.Add(ParseUsage(line))
			}
			want := tc.want
			want.Records = len(tc.lines)
			want.Tokens = None()
			want.Wait, want.Run = Unreported, Unreported
			assert.Equal(t, want, tot)
		})
	}
}

func TestSumUsageTotalsOverWhatWasReported(t *testing.T) {
	t.Parallel()
	us := []Usage{
		ParseUsage("input=10 cache_read=20 output=5 requests=1 max_prompt=30 wait=2s run=10s predicted_usd=0.1 actual_usd=0.2"),
		ParseUsage("input=1 output=1 max_prompt=50 wait=1s predicted_usd=0.000000000000000000001"),
		ParseUsage("wall=1.00s budget=-/100"),
	}
	tot := SumUsage(us)
	assert.Equal(t, 3, tot.Records)
	assert.Equal(t, int64(11), tot.Tokens.Input)
	assert.Equal(t, int64(20), tot.Tokens.CacheRead)
	assert.Equal(t, Unreported, tot.Tokens.CacheWrite, "no record reported it")
	assert.Equal(t, int64(50), tot.Tokens.MaxPrompt, "the largest prompt, not a sum")
	assert.Equal(t, int64(3), tot.Wait)
	assert.Equal(t, int64(10), tot.Run)
	assert.Equal(t, "0.100000000000000000001", tot.Predicted, "decimal sums are exact")
	assert.Equal(t, 2, tot.PredOf)
	assert.Equal(t, "0.2", tot.Actual)
	assert.Equal(t, 1, tot.ActualOf)
	empty := SumUsage(nil)
	require.Equal(t, "", empty.Predicted)
	assert.Equal(t, Unreported, empty.Wait)
}

// A total repriced by one record moves its predicted and charged sums by the record's
// difference, exactly, and nothing else: a record the harness priced keeps its actual as
// its charged figure, a record priced for the first time is counted, one priced no longer
// is taken out, and a total that never counted the record is left as it is.
func TestATotalRepricedMovesOnlyItsPricesByTheDifference(t *testing.T) {
	t.Parallel()
	a := ParseUsage("input=1000000 predicted_usd=0.03")
	b := ParseUsage("input=1000000 predicted_usd=0.5 actual_usd=0.25 actual_by=harness")
	tot := SumUsage([]Usage{a, b})
	require.Equal(t, "0.53", tot.Predicted)
	require.Equal(t, "0.28", tot.Charged)

	a2, b2 := a, b
	a2.Predicted, b2.Predicted = "0.3", "1"
	got := tot.Repriced(a, a2).Repriced(b, b2)
	assert.Equal(t, "1.3", got.Predicted)
	assert.Equal(t, 2, got.PredOf)
	assert.Equal(t, "0.55", got.Charged, "the harness's 0.25 stays b's charged figure")
	assert.Equal(t, 2, got.ChargedOf)
	assert.Equal(t, tot.Tokens, got.Tokens)
	assert.Equal(t, tot.Records, got.Records)

	none := a
	none.Predicted = ""
	gone := tot.Repriced(a, none)
	assert.Equal(t, "0.5", gone.Predicted)
	assert.Equal(t, 1, gone.PredOf)
	assert.Equal(t, "0.25", gone.Charged)
	assert.Equal(t, 1, gone.ChargedOf)
	assert.Equal(t, "", gone.Repriced(b, none).Predicted, "no record left priced: no sum")
	assert.Equal(t, 2, gone.Repriced(none, a).PredOf, "priced for the first time: counted")

	assert.Equal(t, NoTotal(), NoTotal().Repriced(a, a2), "a total that never counted it cannot go below zero")
	bad := tot
	bad.Predicted = "x"
	assert.Equal(t, "x", bad.Repriced(a, a2).Predicted, "a sum that does not read is left")
}
