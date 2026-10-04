package decide

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// counted is a backend that answers the read with one p(defect) and counts its asks.
type counted struct {
	defect float64
	asks   int
}

func (c *counted) Name() string { return "fake" }

func (c *counted) Ask(ctx context.Context, s Schema, state string) (map[string]Answer, Usage, error) {
	c.asks++
	f := Fixed{Table: map[string]FixedAnswer{
		"does_task": {Noul: p(0.8)}, "lines_changed": {Noul: p(0.9)}, "inside_paths": {Noul: p(0.95)}, "defect": {Noul: p(c.defect)},
		"verdict": {Choice: Land, P: map[string]float64{Land: 0.7, Bounce: 0.2, Unsure: 0.1}},
	}}
	return f.Ask(ctx, s, state)
}

var at = time.Date(2026, 10, 2, 22, 0, 0, 0, time.UTC)

// The first read of a flash card routes by p(defect) at the sprint row's two bars
// (0.5 and 0.3, the calibration's): 0.6 bounces with a finding that names p(defect),
// the five answers and a file; 0.4 goes to a strings read; 0.2 lands with no model read.
// Every decision is in the record under the read's op id, and asked again under it, over
// the same card and diff, it is answered from the record with no call.
func TestFirstReadRoutesByTheTwoBars(t *testing.T) {
	t.Parallel()
	bars, err := ParseBars("0.5", "0.3")
	require.NoError(t, err)
	record := filepath.Join(t.TempDir(), "read.jsonl")
	for _, tc := range []struct {
		op     string
		defect float64
		route  string
	}{{"c1.r1@aaa", 0.6, RouteBounce}, {"c2.r1@bbb", 0.4, RouteStrings}, {"c3.r1@ccc", 0.2, RouteLand}, {"c4.r1@ddd", 0.5, RouteBounce}, {"c5.r1@eee", 0.3, RouteStrings}} {
		b := &counted{defect: tc.defect}
		d, route, err := FirstRead(context.Background(), b, bars, "the card "+tc.op, "diff --git a/docs/x.md b/docs/x.md\n", record, tc.op, at)
		require.NoError(t, err)
		assert.Equal(t, tc.route, route, "p(defect) %v", tc.defect)
		_, again, err := FirstRead(context.Background(), b, bars, "the card "+tc.op, "diff --git a/docs/x.md b/docs/x.md\n", record, tc.op, at)
		require.NoError(t, err)
		assert.Equal(t, tc.route, again)
		assert.Equal(t, 1, b.asks, "a recorded op asks nothing")
		assert.Equal(t, tc.op, d.ID)
	}
	ds, err := Load(record)
	require.NoError(t, err)
	require.Len(t, ds, 5)
	d := ds[0]
	f := Finding(d, bars, []string{"docs/x.md"})
	assert.Equal(t, "decide: p(defect)=0.60 at or above the bounce bar 0.50 over docs/x.md; answers: defect=0.60 does_task=0.80 inside_paths=0.95 lines_changed=0.90 verdict=LAND(0.70) (the decide read, docs/SPEC-SPRINT.md section 6)", f)
	assert.Contains(t, Finding(ds[2], bars, nil), "decide: p(defect)=0.20 below the review bar 0.30")
	assert.Contains(t, Finding(ds[1], bars, nil), "decide: p(defect)=0.40 between the bars 0.30 and 0.50")
}

// A strings read's verdict is attached to the decide read it followed, so the record
// trains: ok is LAND, broken is BOUNCE, and no verdict attaches nothing. Calibrate reads
// the decision with its outcome.
func TestSettleAttachesTheStringsReadsVerdict(t *testing.T) {
	t.Parallel()
	bars, err := ParseBars("0.5", "0.3")
	require.NoError(t, err)
	record := filepath.Join(t.TempDir(), "read.jsonl")
	for op, defect := range map[string]float64{"a": 0.45, "b": 0.35, "c": 0.4} {
		_, route, err := FirstRead(context.Background(), &counted{defect: defect}, bars, "card "+op, "diff "+op, record, op, at)
		require.NoError(t, err)
		require.Equal(t, RouteStrings, route)
	}
	require.NoError(t, Settle(record, "a", "broken", "strings read a.r1", at))
	require.NoError(t, Settle(record, "b", "ok", "strings read b.r1", at))
	require.NoError(t, Settle(record, "c", "", "no verdict", at))
	ds, err := Load(record)
	require.NoError(t, err)
	labels := map[string]string{}
	for _, d := range ds {
		if d.Outcome != nil {
			labels[d.ID] = d.Outcome.Label
		}
	}
	assert.Equal(t, map[string]string{"a": Bounce, "b": Land}, labels)
	cal, err := Calibrate(ds, ReadName, "defect", []string{Bounce}, []string{Land})
	require.NoError(t, err)
	assert.Equal(t, []float64{0.45}, cal.Positives)
	assert.Equal(t, []float64{0.35}, cal.Negatives)
	assert.ErrorContains(t, Settle(record, "a", "ok", "again", at), "labelled BOUNCE already")
}

// The bars are probabilities, the review bar at most the bounce bar; every problem is named.
func TestParseBarsNamesEveryProblem(t *testing.T) {
	t.Parallel()
	_, err := ParseBars("x", "1.2")
	assert.ErrorContains(t, err, `decide_bounce "x" is not a decimal; decide_review 1.2 is not a probability in [0, 1]`)
	_, err = ParseBars("0.3", "0.5")
	assert.ErrorContains(t, err, "decide_review 0.5 is above decide_bounce 0.3")
	b, err := ParseBars("0.5", "0.3")
	require.NoError(t, err)
	assert.Equal(t, Bars{Bounce: 0.5, Review: 0.3}, b)
}
