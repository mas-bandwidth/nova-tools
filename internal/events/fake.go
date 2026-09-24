package events

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// FakeStream is the in-memory stream the unit tests run against: the same Store the Redis
// path implements, with the same group semantics -- at-least-once delivery, a pending list
// that survives a "crash", and an ack that is the only thing that retires an entry.
//
// It is STRICT LIKE THE REAL TOOL (AGENTS.md): Emit validates exactly as RedisStore.Emit
// does, and a read under a group nobody created fails with NOGROUP, the way Redis does. A
// lenient fake here would ship the fold broken on the one store that matters.
type FakeStream struct {
	mu      sync.Mutex
	stream  string
	entries []Entry
	groups  map[string]*fakeGroup
	seq     int64

	// Now is the clock Emit stamps an unstamped event with; a test may pin it.
	Now func() time.Time

	// FailEmit, when set, is the error every Emit returns instead of writing. It is
	// the ONLY way to prove the contract the card-path writers are built on -- that a
	// store which refuses an entry does not fail the card, the harvest or the landing
	// -- so the fake carries a way to be broken on purpose (nova-tools #2563 item 1).
	// It fails Emit alone: a fold reading a stream whose writer is broken is a
	// different test.
	FailEmit error
}

type fakeGroup struct {
	delivered int             // how far ">" has walked
	pending   map[string]bool // delivered and not yet acked
}

// NewFakeStream returns an empty stream named for the real one.
func NewFakeStream() *FakeStream {
	return &FakeStream{
		stream: Stream,
		groups: map[string]*fakeGroup{},
		Now:    func() time.Time { return time.Now().UTC() },
	}
}

// StreamName is the stream this fake stands for.
func (f *FakeStream) StreamName() string { return f.stream }

// Close is the Store contract; the fake holds nothing to release.
func (f *FakeStream) Close() error { return nil }

// Len is how many entries the stream holds.
func (f *FakeStream) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.entries)
}

// Emit appends one entry and returns its id, in Redis's `<ms>-<seq>` shape.
func (f *FakeStream) Emit(ctx context.Context, e Event) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if f.FailEmit != nil {
		return "", f.FailEmit
	}
	now := time.Now().UTC()
	if f.Now != nil {
		now = f.Now()
	}
	e = e.Stamp(now)
	if err := e.Validate(); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	id := fmt.Sprintf("%d-0", f.seq)
	f.entries = append(f.entries, Entry{ID: id, Fields: e.Fields()})
	return id, nil
}

// EnsureGroup creates the group at entry 0. A second call is not an error.
func (f *FakeStream) EnsureGroup(ctx context.Context, group string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.groups[group]; !ok {
		f.groups[group] = &fakeGroup{pending: map[string]bool{}}
	}
	return nil
}

func (f *FakeStream) group(name string) (*fakeGroup, error) {
	g, ok := f.groups[name]
	if !ok {
		return nil, fmt.Errorf("NOGROUP No such consumer group '%s' for key name '%s'", name, f.stream)
	}
	return g, nil
}

// Pending returns the group's unacked entries, oldest first: XAUTOCLAIM with MinIdle 0, so
// a restarted fold under any consumer name takes them back.
func (f *FakeStream) Pending(ctx context.Context, group, consumer string, count int) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	g, err := f.group(group)
	if err != nil {
		return nil, err
	}
	if count <= 0 {
		count = 100
	}
	var out []Entry
	for _, e := range f.entries {
		if len(out) >= count {
			break
		}
		if g.pending[e.ID] {
			out = append(out, e)
		}
	}
	return out, nil
}

// Next takes up to count entries the group has never been delivered. block is accepted and
// ignored: the fake never waits, because a test that waits on a clock is a test that fails
// on a loaded bench (AGENTS.md rule 3).
func (f *FakeStream) Next(ctx context.Context, group, consumer string, count int, block time.Duration) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	g, err := f.group(group)
	if err != nil {
		return nil, err
	}
	if count <= 0 {
		count = 100
	}
	var out []Entry
	for g.delivered < len(f.entries) && len(out) < count {
		e := f.entries[g.delivered]
		g.delivered++
		g.pending[e.ID] = true
		out = append(out, e)
	}
	return out, nil
}

// Ack retires the ids from the pending list.
func (f *FakeStream) Ack(ctx context.Context, group string, ids ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	g, err := f.group(group)
	if err != nil {
		return err
	}
	for _, id := range ids {
		delete(g.pending, id)
	}
	return nil
}

// PendingCount is how many entries the group still owes an ack for.
func (f *FakeStream) PendingCount(group string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, ok := f.groups[group]
	if !ok {
		return 0
	}
	return len(g.pending)
}

// Range walks the stream itself from start, inclusive, touching no group.
func (f *FakeStream) Range(ctx context.Context, start string, count int) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if count <= 0 {
		count = 100
	}
	from := int64(0)
	if start != "" && start != "-" {
		n, err := fakeSeq(start)
		if err != nil {
			return nil, err
		}
		from = n
	}
	var out []Entry
	for _, e := range f.entries {
		if len(out) >= count {
			break
		}
		n, err := fakeSeq(e.ID)
		if err != nil {
			return nil, err
		}
		if n >= from {
			out = append(out, e)
		}
	}
	return out, nil
}

// fakeSeq reads the `<n>-0` id this fake mints. An id of any other shape is refused rather
// than guessed at, which is what Redis does with a malformed range bound.
func fakeSeq(id string) (int64, error) {
	base := id
	for i := 0; i < len(id); i++ {
		if id[i] == '-' {
			base = id[:i]
			break
		}
	}
	n, err := strconv.ParseInt(base, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("ERR Invalid stream ID specified as stream command argument: %q", id)
	}
	return n, nil
}
