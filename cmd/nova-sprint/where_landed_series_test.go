package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// where --json --landed-series (docs/SPEC-SPRINT.md, the landed series; the owner,
// 2026-10-05): the cards landed per 10 minutes over the last 24 hours, by the worker of
// the landed attempt, read from the log; absent without the flag, refused without --json.
func TestWhereCarriesTheLandedSeriesOnlyWhenAsked(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1:8")
	ta.ok("add --stream s1 --count 2")
	ta.ok("start")
	ta.landStream("s1", []string{"", ""}, []string{"", ""}, []string{"", ""})

	var plain whereView
	ta.json("where", &plain)
	assert.Nil(t, plain.LandedSeries, "not asked, not carried")

	var v whereView
	ta.json("where --landed-series", &v)
	require.NotNil(t, v.LandedSeries)
	s := v.LandedSeries
	assert.Len(t, s.Fleet, 144)
	assert.Len(t, s.Friends, 144)
	assert.Equal(t, 2, s.Totals.Fleet, "both cards landed, worked on m1")
	assert.Zero(t, s.Totals.Friends)
	assert.Equal(t, map[string]int{"m1": 2}, s.Workers)

	code, _, errs := ta.do("where --landed-series")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--landed-series is a field of the JSON view")
}
