package record

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// FakeStore is the in-memory Store the unit tests run against. It is the same interface the
// real Postgres store implements, so the tests exercise the contract -- idempotent insert,
// filtered list -- without a network, and the RECORD_TEST_PG soak keeps it honest.
type FakeStore struct {
	mu       sync.Mutex
	rows     map[string]Row
	order    []string
	versions map[int]bool

	// Now is the clock Insert stamps a zero RecordedAt with; a test may pin it.
	Now func() time.Time
}

// NewFakeStore returns an empty store.
func NewFakeStore() *FakeStore {
	return &FakeStore{
		rows:     map[string]Row{},
		versions: map[int]bool{},
		Now:      time.Now,
	}
}

// Migrate records the schema version once; a second call keeps the count at one.
func (f *FakeStore) Migrate(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.versions[SchemaVersion] = true
	return nil
}

// Versions is how many distinct schema versions have been applied. One means Migrate was
// idempotent.
func (f *FakeStore) Versions() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.versions)
}

// Rows returns a copy of every stored row in insertion order.
func (f *FakeStore) Rows() []Row {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Row, 0, len(f.order))
	for _, id := range f.order {
		out = append(out, f.rows[id])
	}
	return out
}

// Insert stores the row unless its stream id is already present, which reports false.
func (f *FakeStore) Insert(ctx context.Context, r Row) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.StreamID == "" {
		return false, errors.New("stream id is empty")
	}
	if _, ok := f.rows[r.StreamID]; ok {
		return false, nil
	}
	if r.RecordedAt.IsZero() {
		r.RecordedAt = f.Now()
	}
	f.rows[r.StreamID] = r
	f.order = append(f.order, r.StreamID)
	return true, nil
}

// List applies the filter and returns the rows newest first, matching the Postgres store's
// ORDER BY COALESCE(done_at, recorded_at) DESC, stream_id DESC.
func (f *FakeStore) List(ctx context.Context, filter Filter) ([]Row, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Row
	for _, id := range f.order {
		r := f.rows[id]
		if !filter.Since.IsZero() && sortKey(r).Before(filter.Since) {
			continue
		}
		if filter.Bench != "" && r.Bench != filter.Bench {
			continue
		}
		if filter.Failed && r.Exit == 0 {
			continue
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ki, kj := sortKey(out[i]), sortKey(out[j])
		if !ki.Equal(kj) {
			return ki.After(kj)
		}
		return out[i].StreamID > out[j].StreamID
	})
	return out, nil
}

// Close is a no-op; the fake holds no resources.
func (f *FakeStore) Close() error { return nil }

func sortKey(r Row) time.Time {
	if r.DoneAt != nil {
		return *r.DoneAt
	}
	return r.RecordedAt
}
