package land

import (
	"context"
	"fmt"
	"sync"
)

// Memory is the in-process flaky hash. Observe is the dedup: the first hit
// of a key calls file and stores the issue number; a later hit increments
// lanes_hit and does not call file. The spec's writer is the Redis function
// ns_lane_result, which is not this type. Two processes using two Memories
// do not share a key.
type Memory struct {
	mu   sync.Mutex
	recs map[string]FlakyRecord
}

// NewMemory returns an empty flaky hash.
func NewMemory() *Memory {
	return &Memory{recs: map[string]FlakyRecord{}}
}

// Get returns a copy of the record stored at key.
func (m *Memory) Get(ctx context.Context, key string) (FlakyRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return FlakyRecord{}, false, err
	}
	if m == nil {
		return FlakyRecord{}, false, fmt.Errorf("land: nil flaky memory")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.recs[key]
	return rec, ok, nil
}

// Observe records one hit of key at time at (the caller's clock, already
// formatted). file runs only when the key is new, and it runs before the
// record is published, so a failed file leaves no key and a later hit can
// still file. file must not call Observe on this Memory: the lock is held.
func (m *Memory) Observe(ctx context.Context, key, at string, file func() (int, error)) (FlakyRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return FlakyRecord{}, false, err
	}
	if m == nil {
		return FlakyRecord{}, false, fmt.Errorf("land: nil flaky memory")
	}
	if key == "" || at == "" {
		return FlakyRecord{}, false, fmt.Errorf("land: flaky key and time are required")
	}
	if file == nil {
		return FlakyRecord{}, false, fmt.Errorf("land: filer is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recs == nil {
		m.recs = map[string]FlakyRecord{}
	}
	if rec, ok := m.recs[key]; ok {
		rec.LanesHit++
		rec.LastAt = at
		m.recs[key] = rec
		return rec, false, nil
	}
	n, err := file()
	if err != nil {
		return FlakyRecord{}, false, err
	}
	if n <= 0 {
		return FlakyRecord{}, false, fmt.Errorf("land: filer returned issue %d", n)
	}
	rec := FlakyRecord{FirstSeen: at, LanesHit: 1, Issue: n, LastAt: at}
	m.recs[key] = rec
	return rec, true, nil
}
