package sprint

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStoreRoundTripOverFiveMillisecondsRaisesOneAlarm verifies that:
// - With a fake pinger at 9ms, one judgment is raised on first tick
// - Second tick raises none (already in alarm)
// - With 2ms, episode ends (below half threshold)
func TestStoreRoundTripOverFiveMillisecondsRaisesOneAlarm(t *testing.T) {
	ctx := context.Background()

	// Pinger returns 9ms for all pings
	pinger := &fakePinger{rtts: make([]time.Duration, 20)}
	for i := range pinger.rtts {
		pinger.rtts[i] = 9 * time.Millisecond
	}
	clock := &fakeClock{now: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)}

	meter := newStoreRTTMeter(pinger, clock, 5*time.Millisecond)

	// First tick: should raise alarm
	raised, median, err := meter.Tick(ctx)
	require.NoError(t, err)
	assert.True(t, raised, "should raise alarm on first tick when median > 5ms")
	assert.Equal(t, 9*time.Millisecond, median, "median should be 9ms")
	assert.True(t, meter.alarm, "should be in alarm mode")
	assert.True(t, meter.episode, "should be in episode")

	// Second tick: should NOT raise (already raised)
	raised, median, err = meter.Tick(ctx)
	require.NoError(t, err)
	assert.False(t, raised, "should not raise alarm on second tick")
	assert.Equal(t, 9*time.Millisecond, median, "median should still be 9ms")

	// Change pinger to 2ms to end episode
	for i := range pinger.rtts {
		pinger.rtts[i] = 2 * time.Millisecond
	}
	pinger.Reset()

	// Tick with 2ms: should end episode
	raised, median, err = meter.Tick(ctx)
	require.NoError(t, err)
	assert.False(t, raised, "should not raise alarm")
	assert.Equal(t, 2*time.Millisecond, median, "median should be 2ms")
	assert.False(t, meter.episode, "episode should end when median < 2.5ms")
	assert.False(t, meter.alarm, "alarm should be cleared")
}
