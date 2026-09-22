// Package sprintcol is one column of the unified sprint table (nova-tools #2682).
//
// The table ticks once a second, and a tick is a store read. This package is
// the queue column only: the length of the dealer's stream q:<friend>. It is
// not the renderer (#2681) and it does not poll GitHub. An empty stream is
// zero. A missing store is a refusal. Neither is a reason to list pull requests.
package sprintcol

import (
	"context"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

const (
	// Name is the column this package renders.
	Name = "queue"
	// QueuePrefix is the dealer's stream. The key is q:<friend>.
	QueuePrefix = "q:"
)

// Cell is one friend's queue column. N is the stream length. Zero is an
// empty queue, not a guess, and not a GitHub result.
type Cell struct {
	Column  string
	Subject string
	N       int64
}

// Store is the fixture or the fleet Redis. XLen is the length of one stream:
// zero when the key is absent, an error when the key is some other type.
type Store interface {
	XLen(ctx context.Context, stream string) (int64, error)
}

// GitHub is the poll this column used to be: a list of open pull requests.
// A column source accepts one so the tick can hold it, and does not call it.
type GitHub interface {
	ListOpenPulls(ctx context.Context, repo string) (int, error)
}

// Source is one column. Render reads the store for subject and leaves gh
// uncalled.
type Source interface {
	Name() string
	Render(ctx context.Context, store Store, gh GitHub, subject string) (Cell, error)
}

// Queue is the friend queue column.
type Queue struct{}

// Name is the column name.
func (Queue) Name() string { return Name }

// Render reads q:<friend> with one XLEN. gh is not called. A friend name that
// is not one token is refused before the store, so a colon cannot select a
// different key.
func (Queue) Render(ctx context.Context, store Store, gh GitHub, friend string) (Cell, error) {
	if ctx == nil {
		return Cell{}, fmt.Errorf("queue column needs a context; it does not call GitHub")
	}
	if err := checkFriend(friend); err != nil {
		return Cell{}, err
	}
	if store == nil {
		return Cell{}, fmt.Errorf("queue column has no store; q:%s is not read from GitHub", friend)
	}
	// gh stays unused on purpose. Calling it would make the one-second tick a poll.
	_ = gh
	n, err := store.XLen(ctx, QueuePrefix+friend)
	if err != nil {
		return Cell{}, fmt.Errorf("xlen q:%s: %w", friend, err)
	}
	if n < 0 {
		return Cell{}, fmt.Errorf("xlen q:%s returned %d", friend, n)
	}
	return Cell{Column: Name, Subject: friend, N: n}, nil
}

// checkFriend accepts one token: letters, digits, hyphen, underscore.
// Anything else would change which key XLEN reads.
func checkFriend(name string) error {
	if name == "" {
		return fmt.Errorf("friend name is empty; the queue key is q:<friend>")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return fmt.Errorf("friend name %q is not one token; the queue key is q:<friend> and a colon or space would select another key", name)
		}
	}
	return nil
}

// Redis is the Store over a fleet instance or a miniredis fixture.
type Redis struct {
	rdb *redis.Client
}

// Open dials addr (host:port) and checks the connection once. An empty
// address is refused: this column does not guess a host.
func Open(addr string) (*Redis, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, fmt.Errorf("store address is empty; the queue column reads q:<friend> and does not guess a host")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("store at %s: %w", addr, err)
	}
	return &Redis{rdb: rdb}, nil
}

// Close releases the connection pool.
func (r *Redis) Close() error {
	if r == nil || r.rdb == nil {
		return nil
	}
	return r.rdb.Close()
}

// XLen is one XLEN. A missing stream is zero.
func (r *Redis) XLen(ctx context.Context, stream string) (int64, error) {
	if r == nil || r.rdb == nil {
		return 0, fmt.Errorf("store is not open")
	}
	return r.rdb.XLen(ctx, stream).Result()
}

var (
	_ Store  = (*Redis)(nil)
	_ Source = Queue{}
)
