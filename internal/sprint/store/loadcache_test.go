package store

import (
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store/storetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cachedLoad is a load through the load cache lc, and the read sets it took; fresh
// is the same load with no cache (every table read whole), from the same store.
func cachedLoad(h *harness, lc *LoadCache, tables ...string) (cached, fresh *sprint.Snapshot, readsets int) {
	h.t.Helper()
	c := h.st.clone()
	c.lc = lc
	before := h.m.calls("readset")
	cached, err := c.Load(h.ctx, tables, nil)
	require.NoError(h.t, err)
	readsets = h.m.calls("readset") - before
	f := h.st.clone()
	f.lc = nil
	fresh, err = f.Load(h.ctx, tables, nil)
	require.NoError(h.t, err)
	return cached, fresh, readsets
}

// calls is the exchanges of one kind the Mem answered so far.
func (m *Mem) calls(kind string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Calls[kind]
}

// TestLoadCacheReadsOnlyWhatChanged pins the load cache (loadcache.go): a load of a
// table the cache holds at the shape's revision reads no record, a load after writes
// reads only the records the change stream names, and every load gives what a whole
// read of the same revision gives (storetest.TwinDiff), through a sprint's deal,
// takes, finishes and ticks.
func TestLoadCacheReadsOnlyWhatChanged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(40)
	h.startMachine()
	lc := NewLoadCache()
	tables := []string{sprint.Fleet, sprint.Work}

	// the first load reads the tables whole and keeps them
	cached, fresh, first := cachedLoad(h, lc, tables...)
	require.Empty(t, storetest.TwinDiff(cached, fresh), "the first load is a whole read")
	require.Positive(t, first, "the first load reads the records")

	// nothing written since: no record is read
	cached, fresh, again := cachedLoad(h, lc, tables...)
	require.Empty(t, storetest.TwinDiff(cached, fresh))
	require.Zero(t, again, "a load at the revision the cache holds reads no record")

	// the deal, a take, a finish and ticks write both tables: each load after reads
	// the changed records alone, one read set a table at most here, and agrees with
	// a whole read
	for i := range 4 {
		h.machine()
		if i == 1 {
			h.work("m1")
		}
		cached, fresh, n := cachedLoad(h, lc, tables...)
		require.Empty(t, storetest.TwinDiff(cached, fresh), "after round %d the cached load differs from a whole read", i)
		require.LessOrEqual(t, n, len(tables), "after round %d the load read %d read sets, want the changed records alone", i, n)
	}
}

// TestLoadCacheSnapshotsShareNothing pins the copies (loadcache.go copyCard): a
// caller that changes a card of its snapshot changes neither the cache nor the next
// caller's snapshot.
func TestLoadCacheSnapshotsShareNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(5)
	lc := NewLoadCache()
	first, _, _ := cachedLoad(h, lc, sprint.Work)
	for _, c := range first.Work.Cards() {
		c.Fields["scribbled"] = "yes"
		c.Score = -1
	}
	second, fresh, n := cachedLoad(h, lc, sprint.Work)
	require.Zero(t, n, "the second load is the cache's")
	require.Empty(t, storetest.TwinDiff(second, fresh), "a caller's change to its snapshot reached the cache")
	for _, c := range second.Work.Cards() {
		require.NotContains(t, c.Fields, "scribbled")
	}
}

// TestLoadCacheFencedReadAgrees pins the fenced read (pipeline_load.go) through the
// cache: the read a step without the twin takes gives what a whole fenced read
// gives, at the same generation, and reads no record when nothing changed.
func TestLoadCacheFencedReadAgrees(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(20)
	h.startMachine()
	h.machine()
	c := h.st.clone()
	c.lc = NewLoadCache()
	tables := []string{sprint.Fleet, sprint.Readers}
	_, _, err := c.Fenced(h.ctx, tables, nil, nil)
	require.NoError(t, err)
	before := h.m.calls("readset")
	cached, gen, err := c.Fenced(h.ctx, tables, nil, nil)
	require.NoError(t, err)
	require.Zero(t, h.m.calls("readset")-before, "a fenced read at the revision the cache holds reads no record")
	f := h.st.clone()
	fresh, at, err := f.Fenced(h.ctx, tables, nil, nil)
	require.NoError(t, err)
	require.Equal(t, at, gen)
	require.Empty(t, storetest.TwinDiff(cached, fresh))
}

// TestLoadCacheAfterTheStoreLostWrites pins the cache to the store's history, not its
// revision number (2026-10-10, a cold read's probe): a store that lost its last writes
// (a Redis restart from an AOF that synced every second, an RDB or a backup restore,
// at the same epoch with a lower revision) and wrote a different history since, to
// below, at or past the cache's revision, gives a load what a whole read of the same
// revision gives. The second store stands for the first after its restart: the same
// table names and epoch, the cache of the process that read the first.
func TestLoadCacheAfterTheStoreLostWrites(t *testing.T) {
	t.Parallel()
	for extra := range 7 {
		before := newHarness(t)
		before.setup(20)
		before.startMachine()
		before.machine()
		before.work("m1")
		before.machine()
		lc := NewLoadCache()
		cached, _, _ := cachedLoad(before, lc, sprint.Fleet)
		require.NotEmpty(t, cached.Fleet.Cell("m1", sprint.DoneOK), "the cache holds m1's finished cards")

		after := newHarness(t)
		after.setup(20)
		after.startMachine()
		after.machine()
		after.work("m2") // another history after the point restored
		for range extra {
			after.machine()
		}
		got, fresh, _ := cachedLoad(after, lc, sprint.Fleet)
		require.Empty(t, storetest.TwinDiff(got, fresh),
			"extra=%d: revision %d cached, the store at %d after losing writes: the cached load differs from a whole read", extra, cached.Fleet.Revision, fresh.Fleet.Revision)
	}
}

// TestLoadCacheConcurrentLoads runs loads through one cache from many goroutines at
// once, with the store written between rounds and during one (go test -race): each
// load of a quiet round equals a whole read, and every snapshot is its own.
func TestLoadCacheConcurrentLoads(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(30)
	h.startMachine()
	lc := NewLoadCache()
	tables := []string{sprint.Fleet, sprint.Work}
	load := func() (*sprint.Snapshot, error) {
		c := h.st.clone()
		c.lc = lc
		return c.Load(h.ctx, tables, nil)
	}
	for round := range 4 {
		h.machine()
		if round == 2 {
			h.work("m1")
		}
		var wg sync.WaitGroup
		got := make([]*sprint.Snapshot, 8)
		errs := make([]error, 8)
		for i := range got {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got[i], errs[i] = load()
				if errs[i] == nil {
					for _, c := range got[i].Fleet.Cards() {
						c.Fields["scribbled"] = "by its own caller"
					}
				}
			}()
		}
		wg.Wait()
		f := h.st.clone()
		f.lc = nil
		fresh, err := f.Load(h.ctx, tables, nil)
		require.NoError(t, err)
		for i := range got {
			require.NoError(t, errs[i])
			for _, c := range got[i].Fleet.Cards() {
				delete(c.Fields, "scribbled")
			}
			require.Empty(t, storetest.TwinDiff(got[i], fresh), "round %d, load %d", round, i)
		}
	}
	// loads beside the store's own writes: each ends without error, and the next
	// quiet load equals a whole read
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := load()
			assert.NoError(t, err)
		}()
	}
	h.machine()
	wg.Wait()
	got, err := load()
	require.NoError(t, err)
	f := h.st.clone()
	f.lc = nil
	fresh, err := f.Load(h.ctx, tables, nil)
	require.NoError(t, err)
	require.Empty(t, storetest.TwinDiff(got, fresh))
}
