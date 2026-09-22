// Package sprintcol is one column of the unified sprint table (nova-tools #2682).
//
// The table ticks once a second, and a tick is a store read. This package is
// the queue column only: how many tasks the dealer currently has on
// q:<friend>:front and q:<friend> (nova-tools #2677). It is not the renderer
// (#2681) and it does not poll GitHub. An empty queue is zero. A missing
// store is a refusal. Neither is a reason to list pull requests.
package sprintcol

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

const (
	// Name is the column this package renders.
	Name = "queue"
	// QueuePrefix is the dealer's stream prefix. The bulk key is q:<friend>.
	QueuePrefix = "q:"
	// FrontSuffix is the priority stream beside the bulk one: q:<friend>:front.
	FrontSuffix = ":front"
	// FieldTask is the dealer field that carries the task id. An entry without
	// one is not a queued task.
	FieldTask = "task"
)

// Cell is one friend's queue column. N is the live queue: distinct task ids
// across the front stream and the bulk stream. Zero is an empty queue, not a
// guess, and not a GitHub result.
type Cell struct {
	Column  string
	Subject string
	N       int64
}

// Store is the fixture or the fleet Redis. XRange reads one stream. A missing
// stream is empty. A key of some other type is an error.
type Store interface {
	XRange(ctx context.Context, stream, start, stop string) ([]redis.XMessage, error)
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

// Render counts the live queue on q:<friend>:front and q:<friend>. A task id
// is kept once, the front stream first, so a later copy does not add another.
// An entry with no task id is skipped. gh is not called. A friend name that
// is not one token is refused before the store, so a colon cannot select a
// different key. The count is not XLEN of either stream.
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
	n, err := countLive(ctx, store, friend)
	if err != nil {
		return Cell{}, err
	}
	return Cell{Column: Name, Subject: friend, N: n}, nil
}

// queueKeys are the two streams the dealer writes for this friend: the
// priority stream, then the bulk stream.
func queueKeys(friend string) (front, bulk string) {
	return QueuePrefix + friend + FrontSuffix, QueuePrefix + friend
}

// countLive is the column #2677 specifies. The front stream is read first.
// A task id already seen is not counted again. An entry with no task id is
// not a dealt task. A missing stream is empty, not an error, and not a reason
// to read the other stream's raw length.
func countLive(ctx context.Context, store Store, friend string) (int64, error) {
	front, bulk := queueKeys(friend)
	seen := map[string]struct{}{}
	var n int64
	for _, stream := range []string{front, bulk} {
		msgs, err := store.XRange(ctx, stream, "-", "+")
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue
			}
			return 0, fmt.Errorf("read %s: %w", stream, err)
		}
		for _, m := range msgs {
			id := taskID(valueOf(m.Values, FieldTask))
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			n++
		}
	}
	return n, nil
}

func valueOf(values map[string]any, field string) any {
	if values == nil {
		return nil
	}
	return values[field]
}

func taskID(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case []byte:
		return strings.TrimSpace(string(t))
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

// checkFriend accepts one token: letters, digits, hyphen, underscore.
// Anything else would change which keys are read.
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

// XRange is one XRANGE. A missing stream is empty.
func (r *Redis) XRange(ctx context.Context, stream, start, stop string) ([]redis.XMessage, error) {
	if r == nil || r.rdb == nil {
		return nil, fmt.Errorf("store is not open")
	}
	msgs, err := r.rdb.XRange(ctx, stream, start, stop).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	return msgs, nil
}

var (
	_ Store  = (*Redis)(nil)
	_ Source = Queue{}
)
