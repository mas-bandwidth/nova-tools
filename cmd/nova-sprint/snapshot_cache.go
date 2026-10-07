package main

import (
	"sync"
	"time"
)

// whereSnapshot holds the cached where --json document from the last tick.
type whereSnapshot struct {
	At    time.Time
	Epoch int64
	Data  []byte
}

// snapshotCache is the in-memory cache for where --json snapshots.
type snapshotCache struct {
	mu    sync.Mutex
	snap  *whereSnapshot
	atoms uint64 // tick counter for staleness checks
}

// update replaces the cache with a new snapshot.
func (c *snapshotCache) update(s *whereSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snap = s
	c.atoms++
}

// get returns the current snapshot and atom count.
func (c *snapshotCache) get() (*whereSnapshot, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snap, c.atoms
}

// isStale says whether the cached snapshot is too old (more than two ticks).
func (c *snapshotCache) isStale() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snap == nil {
		return true
	}
	return c.atoms > 2
}

// ageMs returns the age of the snapshot in milliseconds.
func (c *snapshotCache) ageMs() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snap == nil || c.snap.At.IsZero() {
		return 0
	}
	return time.Since(c.snap.At).Milliseconds()
}
