package events

import (
	"context"
	"time"
)

// Entry is one delivered stream entry: the id Redis minted and the fields the writer wrote.
// The id is the fold's primary key, so it is the whole of the idempotency story.
type Entry struct {
	ID     string
	Fields map[string]string
}

// Emitter is the write half: one XADD per card transition. It is an interface so the unit
// tests run against the in-memory FakeStream and the integration test against the fleet
// store, over the same code.
type Emitter interface {
	Emit(ctx context.Context, e Event) (id string, err error)
}

// Reader is the read half the fold uses. It is XREADGROUP, XAUTOCLAIM, XACK and XRANGE and
// nothing else: core types only, no server-side logic (Johnny, section 8).
//
//   - EnsureGroup makes the stream and the group at entry 0, so a fold started after the
//     first writer still reads the whole stream.
//   - Pending claims what this group was delivered and never acked, from any consumer, so a
//     fold that is killed and restarted under a new consumer name loses nothing.
//   - Next takes new entries.
//   - Ack is the commit, and it happens only after the fold's transaction has committed.
//   - Range is the replay --rebuild reads, and it touches no group state.
type Reader interface {
	EnsureGroup(ctx context.Context, group string) error
	Pending(ctx context.Context, group, consumer string, count int) ([]Entry, error)
	Next(ctx context.Context, group, consumer string, count int, block time.Duration) ([]Entry, error)
	Ack(ctx context.Context, group string, ids ...string) error
	Range(ctx context.Context, start string, count int) ([]Entry, error)
}

// Store is a stream that is both halves. The Redis store and the fake both are one.
type Store interface {
	Emitter
	Reader
	Close() error
}
