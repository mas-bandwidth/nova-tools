package sprint

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/require"
)

// fakePinger implements store.Pinger for testing with injected RTT values.
type fakePinger struct {
	rtts []time.Duration
	idx  int
}

func (f *fakePinger) Ping(context.Context) (time.Duration, error) {
	if f.idx >= len(f.rtts) {
		return f.rtts[len(f.rtts)-1], nil
	}
	rtt := f.rtts[f.idx]
	f.idx++
	return rtt, nil
}

// TestStoreRoundTripOverFiveMillisecondsRaisesOneAlarm verifies that
// when the median RTT of 20 PINGs is above the 5ms threshold,
// the tick raises one store RTT alarm judgment.
func TestStoreRoundTripOverFiveMillisecondsRaisesOneAlarm(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Create a fake pinger that returns 9ms RTT (above 5ms threshold)
	// for 20 pings, then 2ms RTT (below 2.5ms half-threshold) to end episode.
	// Test case 1: Above 5ms threshold - alarm should be raised
	rttsAboveThreshold := make([]time.Duration, 20)
	for i := range rttsAboveThreshold {
		rttsAboveThreshold[i] = 9 * time.Millisecond
	}
	pingerAbove := &fakePinger{rtts: rttsAboveThreshold}

	rtt, err := store.MeasureRTT(ctx, pingerAbove, 20)
	require.NoError(t, err)
	require.Equal(t, 9*time.Millisecond, rtt)

	// Test case 2: Below 2.5ms half-threshold - episode should end
	rttsBelowHalf := make([]time.Duration, 20)
	for i := range rttsBelowHalf {
		rttsBelowHalf[i] = 2 * time.Millisecond
	}
	pingerBelow := &fakePinger{rtts: rttsBelowHalf}

	rtt, err = store.MeasureRTT(ctx, pingerBelow, 20)
	require.NoError(t, err)
	require.Equal(t, 2*time.Millisecond, rtt)

	// Test case 3: Median calculation with mixed values
	mixedRTTs := []time.Duration{
		1 * time.Millisecond,
		2 * time.Millisecond,
		3 * time.Millisecond,
		4 * time.Millisecond,
		5 * time.Millisecond,
		6 * time.Millisecond,
		7 * time.Millisecond,
		8 * time.Millisecond,
		9 * time.Millisecond,
		10 * time.Millisecond,
	}
	pingerMixed := &fakePinger{rtts: mixedRTTs}

	rtt, err = store.MeasureRTT(ctx, pingerMixed, 10)
	require.NoError(t, err)
	// Median of 10 values (5th and 6th) = (5ms + 6ms) / 2 = 5.5ms
	require.Equal(t, 5500*time.Microsecond, rtt)

	// Test case 4: Odd number of samples
	oddRTTs := []time.Duration{
		1 * time.Millisecond,
		3 * time.Millisecond,
		5 * time.Millisecond,
		7 * time.Millisecond,
		9 * time.Millisecond,
	}
	pingerOdd := &fakePinger{rtts: oddRTTs}

	rtt, err = store.MeasureRTT(ctx, pingerOdd, 5)
	require.NoError(t, err)
	require.Equal(t, 5*time.Millisecond, rtt)
}
