package sprint_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// store-latency-alarm-bb.w3: the store round trip is shown and alarms above 5 ms.
func TestStoreRoundTripOverFiveMillisecondsRaisesOneAlarm(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := store.NewMem()
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	h := &rttHarness{t: t, ctx: ctx, m: m, now: now, mu: &sync.Mutex{}}
	st := &store.Store{B: m, Names: sprint.Names{Prefix: "t-"}, Actor: "tester",
		Now: func() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.now }}
	require.NoError(t, st.Init(ctx))
	require.NoError(t, m.RowsAdd(ctx, "t-readers", []string{"reader-a", "reader-b"}))
	require.NoError(t, m.SetCoordinator(ctx, "tester"))
	zero := 0.0
	st.Beat(ctx, "m1", &zero, hostload.Source{})
	st.BeatReaders(ctx)
	
	// Set machine to RUNNING
	machineJSON, _ := json.Marshal(store.Machine{State: store.Running})
	require.NoError(t, m.SetKey(ctx, "machine", string(machineJSON)))
	
	h.st = st

	// Before any measurement, no RTT alarm
	s := h.snap()
	assert.False(t, s.HasStoreRTT, "before RTT measurement")
	assert.Empty(t, s.Open, "before RTT measurement")

	// Set up fake pinger at 9 ms - simulates slow store
	st.Pinger = func(ctx context.Context, count int) ([]time.Duration, error) {
		ones := make([]time.Duration, 20)
		for i := range ones {
			ones[i] = 9 * time.Millisecond
		}
		return ones, nil
	}

	// Run the RTT measurement - should record the slow RTT
	_, err := st.MeasureStoreRTT(ctx)
	require.NoError(t, err)
	
	// Check immediate results after measurement (no tick yet)
	s = h.snap()
	assert.True(t, s.HasStoreRTT, "after RTT measurement")
	assert.GreaterOrEqual(t, s.StoreRTTP50MS, 8.0, "p50 after measurement")
	assert.LessOrEqual(t, s.StoreRTTP50MS, 10.0, "p50 after measurement")
	assert.Empty(t, s.Open, "before tick")

	// Tick should raise alarm
	h.tick(10 * time.Second)
	s = h.snap()
	assert.Len(t, s.Open, 1, "alarm raised after tick")
	assert.Equal(t, sprint.NAlarmSlowStore, s.Open[0].Note.Type, "alarm type")

	// Second tick with same RTT should not raise another alarm (same episode)
	h.tick(10 * time.Second)
	s = h.snap()
	assert.Len(t, s.Open, 1, "still one alarm after second tick")

	// Clear RTT record to test alarm clearing
	require.NoError(t, m.SetKey(ctx, "store_rtt", ""))

	// RTT drops below 2.5ms (half of 5ms bar), episode should end
	st.Pinger = func(ctx context.Context, count int) ([]time.Duration, error) {
		ones := make([]time.Duration, 20)
		for i := range ones {
			ones[i] = 2 * time.Millisecond
		}
		return ones, nil
	}

	_, err = st.MeasureStoreRTT(ctx)
	require.NoError(t, err)
	h.tick(10 * time.Second)
	s = h.snap()
	
	assert.True(t, s.HasStoreRTT, "RTT still available after fast measurement")
	assert.Less(t, s.StoreRTTP50MS, 3.0, "p50 after fast measurement")
	assert.Empty(t, s.Open, "alarm cleared when RTT falls below threshold")
}

type rttHarness struct {
	t    *testing.T
	ctx  context.Context
	m    *store.Mem
	st   *store.Store
	mu   *sync.Mutex
	now  time.Time
}

func (h *rttHarness) tick(d time.Duration) {
	h.mu.Lock()
	h.now = h.now.Add(d)
	h.mu.Unlock()
	st := h.st
	zero := 0.0
	st.BeatReaders(h.ctx)
	st.Beat(h.ctx, "m1", &zero, hostload.Source{})
	_, err := st.Tick(h.ctx)
	require.NoError(h.t, err)
}

func (h *rttHarness) snap() *sprint.Snapshot {
	st := h.st
	s, err := st.Load(h.ctx, store.All, nil)
	require.NoError(h.t, err)
	return s
}
