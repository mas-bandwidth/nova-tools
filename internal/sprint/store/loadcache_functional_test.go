//go:build functional

package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store/storetest"
	"github.com/stretchr/testify/require"
)

// TestRedisLoadCacheAgreesWithAWholeRead runs the load cache (loadcache.go) on a real
// Redis with the table layer's functions: the pipelined fenced read
// (PipelinedLoadWithFence: the marks before the whole read, the catch-up before the
// pipeline, the kept records after it) and Load, after a deal, a worker's takes and
// finishes and ticks, each equal to a whole read of the same generation, with the
// change stream's ids as the marks; and a load at the cached revision reads no record.
func TestRedisLoadCacheAgreesWithAWholeRead(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	h.setup(20)
	h.startMachine()
	lc := NewLoadCache()
	tables := []string{sprint.Fleet, sprint.Work, sprint.Readers}
	check := func(round int) {
		t.Helper()
		c := h.st.clone()
		c.lc = lc
		cached, gen, err := c.Fenced(h.ctx, tables, nil, nil)
		require.NoError(t, err)
		f := h.st.clone()
		f.lc = nil
		fresh, at, err := f.Fenced(h.ctx, tables, nil, nil)
		require.NoError(t, err)
		require.Equal(t, at, gen, "round %d: no write between the two reads", round)
		require.Empty(t, storetest.TwinDiff(cached, fresh), "round %d: the fenced read through the cache", round)
		// a load is unfenced: it is held to an unfenced whole read (no queue length, no
		// machine state)
		loaded, err := c.Load(h.ctx, tables, nil)
		require.NoError(t, err)
		freshLoad, err := f.Load(h.ctx, tables, nil)
		require.NoError(t, err)
		require.Empty(t, storetest.TwinDiff(loaded, freshLoad), "round %d: the load through the cache", round)
		rows := c.stats().rows.Load()
		_, err = c.Load(h.ctx, tables, nil)
		require.NoError(t, err)
		require.Zero(t, c.stats().rows.Load()-rows, "round %d: a load at the cached revision reads no record", round)
	}
	check(0)
	for round := 1; round <= 4; round++ {
		h.machine()
		if round == 2 {
			h.work("m1")
		}
		check(round)
	}
}
