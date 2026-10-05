package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// where --json carries landedSeries (cards landed per 10-minute bucket over 24 hours,
// split between friends and fleet, where-landed-series.w1).
func TestWhereCarriesLandedSeries(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1:8")
	ta.ok("add --stream s1 --count 2 --brief-file " + proBriefFile(t))
	ta.ok("start")
	ta.landStream("s1", []string{"input=10 actual_usd=1 actual_by=harness", "input=10 actual_usd=2 actual_by=harness"}, []string{"", ""}, []string{"", ""})

	out := ta.ok("where --json")
	var doc struct {
		LandedSeries *sprint.LandedSeries `json:"landedSeries"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &doc))
	require.NotNil(t, doc.LandedSeries)
	assert.Equal(t, 144, doc.LandedSeries.Buckets)
	assert.Equal(t, 600, doc.LandedSeries.BucketSeconds)
	assert.Equal(t, 2, doc.LandedSeries.Totals.Fleet+doc.LandedSeries.Totals.Friends+doc.LandedSeries.Totals.Unknown)
}
