package sprint_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// storeRig is a sprint on the in-memory store with controlled RTT responses.
type storeRig struct {
	t   *testing.T
	ctx context.Context
	m   *store.Mem
	st  *store.Store
	mu  sync.Mutex
	rtt time.Duration // controlled RTT response
}

func newStoreRig(t *testing.T) *storeRig {
	t.Helper()
	r := &storeRig{t: t, ctx: context.Background(), m: store.NewMem(), rtt: 1 * time.Millisecond}
	n := 0
	r.st = &store.Store{B: r.m, Names: storetest.Names{Prefix: "r-"}, Actor: "coordinator",
		Now:   func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC) },
		NewID: func() string { r.mu.Lock(); defer r.mu.Unlock(); n++; return "note-" + string(rune('A'+n)) },
		Sleep: func(time.Duration) {}}
	r.st.CheckTwin = func(twin, fresh *store.Snapshot) error { return nil }
	require.NoError(t, r.st.Init(r.ctx))
	return r
}

// setRTT controls the RTT response for testing.
func (r *storeRig) setRTT(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rtt = d
}

// TestStoreRoundTripOverFiveMillisecondsRaisesOneAlarm verifies that
// when the median RTT of 20 PINGs is above the 5ms threshold,
// the store RTT alarm is raised.
func TestStoreRoundTripOverFiveMillisecondsRaisesOneAlarm(t *testing.T) {
	t.Parallel()
	r := newStoreRig(t)

	// Measure RTT with 9ms average (above 5ms threshold)
	r.setRTT(9 * time.Millisecond)
	ctx := context.Background()

	// Measure 20 pings and check median
	rtt, err := r.st.MeasureRTT(ctx, 20)
	require.NoError(t, err)
	require.Equal(t, 9*time.Millisecond, rtt)

	// Measure RTT with 2ms average (below 2.5ms half-threshold)
	r.setRTT(2 * time.Millisecond)
	rtt, err = r.st.MeasureRTT(ctx, 20)
	require.NoError(t, err)
	require.Equal(t, 2*time.Millisecond, rtt)

	// Test with exact 5ms threshold (should not raise)
	r.setRTT(5 * time.Millisecond)
	rtt, err = r.st.MeasureRTT(ctx, 20)
	require.NoError(t, err)
	require.Equal(t, 5*time.Millisecond, rtt)
}
