package main

import (
	"encoding/json"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// where --json carries the series, and the dashboard's one read of that verb
// carries it too. The text frame does not.
func TestWhereJSONCarriesTheLandedSeries(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	var v struct {
		LandedSeries sprint.LandedSeries `json:"landedSeries"`
	}
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json")), &v))
	require.Equal(t, int64(sprint.LandedBucketSeconds), v.LandedSeries.BucketSeconds)
	require.Equal(t, sprint.LandedBuckets, v.LandedSeries.Buckets)
	require.Len(t, v.LandedSeries.Friends, sprint.LandedBuckets)
	require.Len(t, v.LandedSeries.Fleet, sprint.LandedBuckets)
	require.NotContains(t, ta.ok("where"), "landedSeries")

	raw, err := ta.a.whereJSON("", false)
	require.NoError(t, err)
	var again struct {
		LandedSeries sprint.LandedSeries `json:"landedSeries"`
	}
	require.NoError(t, json.Unmarshal(raw, &again))
	require.Len(t, again.LandedSeries.Friends, sprint.LandedBuckets)
	require.Len(t, again.LandedSeries.Fleet, sprint.LandedBuckets)

	help := ta.ok("where -h")
	require.Contains(t, help, "landedSeries")
	require.Contains(t, help, "effect:")
}
