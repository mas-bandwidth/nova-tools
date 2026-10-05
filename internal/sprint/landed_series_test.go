package sprint_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

type fakeLogStore struct {
	lines []sprint.Line
}

func (f fakeLogStore) Log(ctx context.Context) ([]sprint.Line, error) {
	return f.lines, nil
}

func TestTheLandedSeriesCountsEachCardOnceByItsWorker(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	t0 := now.Add(-30 * time.Minute)

	moves := []sprint.Line{
		// Worker attempt: card "s1-1.w1" moved to "friend.emma:ok"
		{
			Kind: sprint.LineMove,
			At:   t0,
			Card: "s1-1.w1",
			To:   "friend.emma:ok",
		},
		// Card "s1-1" moved from "sprint:working" to "sprint:landed" -> lands under friends (emma)
		{
			Kind:  sprint.LineMove,
			At:    t0.Add(time.Minute),
			Table: sprint.Work,
			Card:  "s1-1",
			From:  "sprint:working",
			To:    "sprint:landed",
		},
		// Card "s1-1" moved from "sprint:landed" to "sprint:landed" again -> ignored (not double counted)
		{
			Kind:  sprint.LineMove,
			At:    t0.Add(2 * time.Minute),
			Table: sprint.Work,
			Card:  "s1-1",
			From:  "sprint:landed",
			To:    "sprint:landed",
		},
		// Sentinel card "sentinel-1" moved from "waiting" to "sprint:landed" -> ignored (sentinel release is not work)
		{
			Kind:  sprint.LineMove,
			At:    t0.Add(3 * time.Minute),
			Table: sprint.Work,
			Card:  "sentinel-1",
			From:  "waiting",
			To:    "sprint:landed",
		},
		// Worker attempt: card "s1-2.w1" moved to "worker-1:ok"
		{
			Kind: sprint.LineMove,
			At:   t0.Add(4 * time.Minute),
			Card: "s1-2.w1",
			To:   "worker-1:ok",
		},
		// Card "s1-2" moved from "sprint:working" to "sprint:landed" -> lands under fleet (worker-1)
		{
			Kind:  sprint.LineMove,
			At:    t0.Add(5 * time.Minute),
			Table: sprint.Work,
			Card:  "s1-2",
			From:  "sprint:working",
			To:    "sprint:landed",
		},
	}

	st := fakeLogStore{lines: moves}
	series, err := sprint.LandedSeriesFrom(context.Background(), st, now)
	require.NoError(t, err)

	assert.Equal(t, 144, series.Buckets)
	assert.Equal(t, 600, series.BucketSeconds)
	assert.Equal(t, now.Format(time.RFC3339), series.Generated)
	assert.Equal(t, now.Unix(), series.GeneratedEpoch)

	// Check totals
	assert.Equal(t, sprint.SeriesTotals{Friends: 1, Fleet: 1, Unknown: 0}, series.Totals)
	assert.Equal(t, sprint.SeriesLastHour{Friends: 1, Fleet: 1}, series.LastHour)

	// Check workers
	assert.Equal(t, map[string]int{"emma": 1, "worker-1": 1}, series.Workers)

	// Verify bucket counts: each card counted once in the appropriate series
	var totalFriends, totalFleet int
	for _, c := range series.Friends {
		totalFriends += c
	}
	for _, c := range series.Fleet {
		totalFleet += c
	}
	assert.Equal(t, 1, totalFriends, "friends series total")
	assert.Equal(t, 1, totalFleet, "fleet series total")

	// Specifically verify bucket indices
	assert.Equal(t, 1, series.Friends[140], "s1-1 landed in bucket 140")
	assert.Equal(t, 1, series.Fleet[140], "s1-2 landed in bucket 140")
}
