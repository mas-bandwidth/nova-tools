package store

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// What a tick costs, part by part (the owner's requirement of 2026-09-30:
// "the whole intent is sub-second ticks"): the wall time of each part, the
// round trips it made to the store, the whole-table reads it took and the
// records those reads and its other reads brought back. Each part's numbers
// are the counters' difference across it (PartTime).

// Tripper is a backend that counts its round trips to the store: every
// command or pipeline sent and answered is one.
type Tripper interface {
	Trips() int64
}

// Stats is a store's counters of what its reads cost. A Store's copies (its
// pinned ones) share it.
type Stats struct {
	reads atomic.Int64 // whole reads of a table (its shape, its cells, its records)
	rows  atomic.Int64 // records read, by any read set
	twin  atomic.Int64 // reads a step answered from the tick's twin instead of the store
	stale atomic.Int64 // reads of the twin refused as stale: another writer wrote since
	// mismatch is the tables whose records, caught up, did not add up to the
	// store's counts of the same revision: each read whole (twin.go)
	mismatch atomic.Int64
	// notes is what the store's reads met that the tick says once (a grant
	// missing): each said by the next tick, and not again.
	mu    sync.Mutex
	notes []string
	said  map[string]bool
}

// note records a thing to say once.
func (s *Stats) note(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.said[text] {
		return
	}
	if s.said == nil {
		s.said = map[string]bool{}
	}
	s.said[text] = true
	s.notes = append(s.notes, text)
}

// takeNotes is the notes not yet said.
func (s *Stats) takeNotes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.notes
	s.notes = nil
	return out
}

// lazyMu guards the store's lazily made fields (Stats, tw) and every copy of
// the store that carries them (clone): a tick makes them on the store the run
// loop keeps while a verb on another goroutine pins a copy of it. It is held
// for a pointer's test and set, or a struct copy, never across a backend call
// or a step; a store pinned from the one the tick made shares the pointers.
var lazyMu sync.Mutex

// clone is a copy of the store taken while no lazy field is being made.
func (st *Store) clone() Store {
	lazyMu.Lock()
	defer lazyMu.Unlock()
	return *st
}

// stats is the store's counters, made on first use.
func (st *Store) stats() *Stats {
	lazyMu.Lock()
	defer lazyMu.Unlock()
	if st.Stats == nil {
		st.Stats = &Stats{}
	}
	return st.Stats
}

// NoteStaleSet is the process's set of stale refusals already named in a note,
// made on first use; the store's clones share it (clone copies the reference),
// so the tick names each stale refusal once a process, never once a tick.
func (st *Store) NoteStaleSet() map[string]bool {
	lazyMu.Lock()
	defer lazyMu.Unlock()
	if st.notedStale == nil {
		st.notedStale = map[string]bool{}
	}
	return st.notedStale
}

// MarkNotedStale records the stale-refusal notes of a plan as written, once
// the step that planned them has committed: the next tick's deal skips them.
// Called only after a successful commit, so a plan retried before its commit
// does not mark refusals that were never named.
func (st *Store) MarkNotedStale(notes []sprint.Note) {
	if len(notes) == 0 {
		return
	}
	set := st.NoteStaleSet()
	for _, n := range notes {
		if n.Type == sprint.NProviderStale {
			set[n.What] = true
		}
	}
}

// trips is the backend's round trips so far; 0 when it does not count them.
func (st *Store) trips() int64 {
	if t, ok := st.B.(Tripper); ok {
		return t.Trips()
	}
	if t, ok := st.root.(Tripper); ok {
		return t.Trips()
	}
	return 0
}

// meter is a part's counters at its start.
type meter struct {
	st                           *Store
	began                        time.Time
	trips, reads, rows, stl, mis int64
}

// meter starts measuring a part.
func (st *Store) meter() meter {
	s := st.stats()
	return meter{st: st, began: time.Now(), trips: st.trips(), reads: s.reads.Load(), rows: s.rows.Load(), stl: s.stale.Load(), mis: s.mismatch.Load()}
}

// part is what the part cost since its meter began.
func (m meter) part(table, name string) PartTime {
	s := m.st.stats()
	return PartTime{Table: table, Name: name, Took: time.Since(m.began), Trips: m.st.trips() - m.trips,
		Reads: s.reads.Load() - m.reads, Rows: s.rows.Load() - m.rows, Stale: s.stale.Load() - m.stl, Mismatch: s.mismatch.Load() - m.mis}
}

// Cost is a tick's times summed: its wall time and every part's trips, reads
// and rows.
func (r TickResult) Cost() PartTime {
	out := PartTime{Name: "tick", Took: r.Took}
	for _, p := range r.Times {
		out.Trips += p.Trips
		out.Reads += p.Reads
		out.Rows += p.Rows
		out.Stale += p.Stale
		out.Mismatch += p.Mismatch
	}
	return out
}

// TimesLine is the tick's cost in one line: the whole, then each part as
// table/name, its time, its round trips (t), whole-table reads (r) and
// records read (n).
func (r TickResult) TimesLine() string {
	c := r.Cost()
	var b strings.Builder
	fmt.Fprintf(&b, "TIMES %dms trips=%d reads=%d rows=%d stale=%d mismatch=%d:", c.Took.Milliseconds(), c.Trips, c.Reads, c.Rows, c.Stale, c.Mismatch)
	for _, p := range r.Times {
		name := p.Name
		if p.Table != "" {
			name = p.Table + "/" + p.Name
		}
		fmt.Fprintf(&b, " %s=%dms/%dt/%dr/%dn", strings.ReplaceAll(name, " ", "-"), p.Took.Milliseconds(), p.Trips, p.Reads, p.Rows)
	}
	return b.String()
}

// tripHook counts a client's round trips: each command, and each pipeline or
// transaction, sent and answered.
type tripHook struct{ n *atomic.Int64 }

func (h tripHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h tripHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.n.Add(1)
		return next(ctx, cmd)
	}
}

func (h tripHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.n.Add(1)
		return next(ctx, cmds)
	}
}

// CountTrips has the backend count its round trips from now on (Trips).
func (r *Redis) CountTrips() {
	if r.trips != nil {
		return
	}
	r.trips = &atomic.Int64{}
	r.C.AddHook(tripHook{r.trips})
}

// Trips is the round trips the backend's client made since CountTrips.
func (r *Redis) Trips() int64 {
	if r.trips == nil {
		return 0
	}
	return r.trips.Load()
}
