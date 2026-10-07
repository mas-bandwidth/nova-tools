package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// store-latency-row-r.w2 (docs/SPEC-SPRINT.md section 14): the server times one store round
// trip every 10 s by the injected clock and keeps the samples of the last minute in the store;
// where reads their p50 and p99 and prints them on its store line.
func TestWhereReportsTheStoreRoundTrip(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	st, err := h.st.Pinned(h.ctx)
	require.NoError(t, err)

	// before the server measures, where has no round trip and no store line
	f, err := st.WhereFacts(h.ctx, 0)
	require.NoError(t, err)
	assert.False(t, f.HasStoreRTT)
	assert.Empty(t, f.StoreLine())

	// six round trips, 10 s apart: 10, 20, ... 60 ms
	for i := 1; i <= 6; i++ {
		require.NoError(t, st.RecordStoreRTT(h.ctx, time.Duration(i*10)*time.Millisecond))
		h.tick(StoreRTTEvery)
	}
	f, err = st.WhereFacts(h.ctx, 0)
	require.NoError(t, err)
	assert.True(t, f.HasStoreRTT)
	assert.Equal(t, 40.0, f.StoreRTTP50MS, "nearest rank p50 of 10..60 ms is the sample at index 3")
	assert.Equal(t, 60.0, f.StoreRTTP99MS)
	assert.Equal(t, "store: rtt p50=40ms p99=60ms", f.StoreLine())

	// a seventh, 70 s after the first: the first falls out of the minute
	h.tick(StoreRTTEvery)
	require.NoError(t, st.RecordStoreRTT(h.ctx, 70*time.Millisecond))
	f, err = st.WhereFacts(h.ctx, 0)
	require.NoError(t, err)
	assert.Equal(t, 50.0, f.StoreRTTP50MS, "20..70 ms kept")
	assert.Equal(t, 70.0, f.StoreRTTP99MS)
	assert.Equal(t, "store: rtt p50=50ms p99=70ms", f.StoreLine())

	// a server that stopped measuring: past the minute, where shows none
	h.tick(StoreRTTWindow + time.Second)
	f, err = st.WhereFacts(h.ctx, 0)
	require.NoError(t, err)
	assert.False(t, f.HasStoreRTT)
	assert.Empty(t, f.StoreLine())

	// MeasureStoreRTT times the round trip by the injected clock: a clock that moves
	// 250 µs on each read times 250 µs, kept below the millisecond
	m := *st
	base, step := h.now, 0
	m.Now = func() time.Time { step++; return base.Add(time.Duration(step) * 250 * time.Microsecond) }
	d, err := m.MeasureStoreRTT(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, 250*time.Microsecond, d)
	f, err = m.WhereFacts(h.ctx, 0)
	require.NoError(t, err)
	assert.True(t, f.HasStoreRTT)
	assert.Equal(t, 0.25, f.StoreRTTP50MS)
	assert.Equal(t, 0.25, f.StoreRTTP99MS)
	assert.Equal(t, "store: rtt p50=0.25ms p99=0.25ms", f.StoreLine())
}
