// Package sprintcol is one column of the unified sprint table (nova-tools #2682).
//
// The table ticks once a second, and a tick is a store read. This package is
// the queue column only: how many tasks the dealer still has queued for one
// friend (nova-tools #2677). The dealer places a task on q:<friend>:front or
// q:<friend> and does not delete the stream entry on close, so the streams are
// history and grow with every task the friend was ever dealt. Current owner
// and state live on the hash task:<id>. The dealer also keeps the live index
// sprint:<sprint>:idx:<friend>:open, a set of task ids, in the same pipelined
// batch as every HSET and XADD (friend-queue push/take/done/cancel; `friend-queue
// reindex` rebuilds it). This column reads that set, not the streams, so a
// render costs the live queue, never the history.
//
// It is not the renderer (#2681) and it does not poll GitHub. An empty queue
// is zero. A missing store is a refusal. Neither is a reason to list pull
// requests.
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
	// The streams are history; this column does not read them.
	QueuePrefix = "q:"
	// FrontSuffix is the priority stream beside the bulk one: q:<friend>:front.
	FrontSuffix = ":front"

	// indexPrefix and openSuffix make the dealer's live index:
	// sprint:<sprint>:idx:<friend>:open, the task ids this friend owns in
	// state open. The dealer SADDs on push and SREMs on take, done, cancel,
	// and reassign.
	indexPrefix = "sprint:"
	indexInfix  = ":idx:"
	openSuffix  = ":open"

	// taskPrefix is the dealer's hash key. owner and state on that hash are
	// current; the index is checked against it so a member the dealer failed
	// to remove does not count.
	taskPrefix = "task:"
	fieldOwner = "owner"
	fieldState = "state"
	// stateOpen is the queued state. working is leased. closed is done.
	stateOpen = "open"
)

// Cell is one friend's queue column. N is the live queue: members of the
// dealer's open index whose task:<id> hash still names this friend as owner
// and state open. Zero is an empty queue, not a guess, and not a GitHub
// result.
type Cell struct {
	Column  string
	Subject string
	N       int64
}

// Store is the fixture or the fleet Redis. SMembers reads the live index.
// HMGetEach reads owner and state of every listed hash in one round trip. A
// missing set is empty. A missing hash is not a queued task. A key of some
// other type is an error.
type Store interface {
	SMembers(ctx context.Context, key string) ([]string, error)
	HMGetEach(ctx context.Context, keys []string, fields ...string) ([][]any, error)
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

// Queue is the friend queue column for one sprint. Sprint names the dealer's
// index: sprint:<Sprint>:idx:<friend>:open.
type Queue struct {
	Sprint string
}

// Name is the column name.
func (Queue) Name() string { return Name }

// Render counts the live queue from the dealer's open index. It is two round
// trips whatever the history: one SMEMBERS of sprint:<sprint>:idx:<friend>:open
// and one pipelined HMGET of owner and state per member. A member counts only
// when task:<id> has owner equal to this friend and state open, so a stale
// member (closed, working, reassigned, or missing hash) does not. The streams
// q:<friend>:front and q:<friend> are not read: they keep every task ever
// dealt. gh is not called. A friend or sprint name that is not one token is
// refused before the store, so a colon cannot select a different key.
func (q Queue) Render(ctx context.Context, store Store, gh GitHub, friend string) (Cell, error) {
	if ctx == nil {
		return Cell{}, fmt.Errorf("queue column needs a context; it does not call GitHub")
	}
	if err := checkToken("friend", friend); err != nil {
		return Cell{}, err
	}
	if err := checkToken("sprint", q.Sprint); err != nil {
		return Cell{}, err
	}
	if store == nil {
		return Cell{}, fmt.Errorf("queue column has no store; %s is not read from GitHub", OpenIndexKey(q.Sprint, friend))
	}
	// gh stays unused on purpose. Calling it would make the one-second tick a poll.
	_ = gh
	n, err := countLive(ctx, store, q.Sprint, friend)
	if err != nil {
		return Cell{}, err
	}
	return Cell{Column: Name, Subject: friend, N: n}, nil
}

// OpenIndexKey is the dealer's live index for one friend in one sprint.
func OpenIndexKey(sprint, friend string) string {
	return indexPrefix + sprint + indexInfix + friend + openSuffix
}

// countLive reads the open index, then every member's hash in one pipeline.
// Work and round trips are proportional to the index, not to the streams.
func countLive(ctx context.Context, store Store, sprint, friend string) (int64, error) {
	index := OpenIndexKey(sprint, friend)
	members, err := store.SMembers(ctx, index)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, nil
		}
		return 0, fmt.Errorf("read %s: %w", index, err)
	}
	seen := make(map[string]struct{}, len(members))
	ids := make([]string, 0, len(members))
	keys := make([]string, 0, len(members))
	for _, m := range members {
		id := strings.TrimSpace(m)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
		keys = append(keys, taskPrefix+id)
	}
	if len(keys) == 0 {
		return 0, nil
	}
	rows, err := store.HMGetEach(ctx, keys, fieldOwner, fieldState)
	if err != nil {
		return 0, fmt.Errorf("read task hashes for %s: %w", index, err)
	}
	if len(rows) != len(keys) {
		return 0, fmt.Errorf("read task hashes for %s: %d replies for %d keys", index, len(rows), len(keys))
	}
	var n int64
	for i := range ids {
		owner := fieldAt(rows[i], 0)
		state := fieldAt(rows[i], 1)
		if owner == friend && state == stateOpen {
			n++
		}
	}
	return n, nil
}

func fieldAt(vals []any, i int) string {
	if vals == nil || i < 0 || i >= len(vals) {
		return ""
	}
	switch t := vals[i].(type) {
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

// checkToken accepts one token: letters, digits, hyphen, underscore, dot.
// Anything else would change which keys are read.
func checkToken(what, name string) error {
	if name == "" {
		return fmt.Errorf("%s name is empty; the queue index is sprint:<sprint>:idx:<friend>:open", what)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return fmt.Errorf("%s name %q is not one token; the queue index is sprint:<sprint>:idx:<friend>:open and a colon or space would select another key", what, name)
		}
	}
	return nil
}

// Redis is the Store over a fleet instance or a miniredis fixture.
type Redis struct {
	rdb *redis.Client
}

// Open names addr (host:port) and sends nothing; the first read dials and
// reports an unreachable store (#3277). An empty address is refused: this
// column does not guess a host.
func Open(addr string) (*Redis, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, fmt.Errorf("store address is empty; the queue column reads the dealer's index and does not guess a host")
	}
	return &Redis{rdb: redis.NewClient(&redis.Options{Addr: addr})}, nil
}

// Close releases the connection pool.
func (r *Redis) Close() error {
	if r == nil || r.rdb == nil {
		return nil
	}
	return r.rdb.Close()
}

// SMembers is one SMEMBERS. A missing set is empty. A key of another type is
// an error.
func (r *Redis) SMembers(ctx context.Context, key string) ([]string, error) {
	if r == nil || r.rdb == nil {
		return nil, fmt.Errorf("store is not open")
	}
	members, err := r.rdb.SMembers(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	return members, nil
}

// HMGetEach is one pipeline: an HMGET of fields on every key, in order. A
// missing hash gives nil fields, not an error: the task is not queued. A key
// of another type is an error.
func (r *Redis) HMGetEach(ctx context.Context, keys []string, fields ...string) ([][]any, error) {
	if r == nil || r.rdb == nil {
		return nil, fmt.Errorf("store is not open")
	}
	if len(keys) == 0 {
		return nil, nil
	}
	cmds := make([]*redis.SliceCmd, len(keys))
	_, err := r.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		for i, k := range keys {
			cmds[i] = p.HMGet(ctx, k, fields...)
		}
		return nil
	})
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	out := make([][]any, len(keys))
	for i, c := range cmds {
		vals, cerr := c.Result()
		if cerr != nil && !errors.Is(cerr, redis.Nil) {
			return nil, fmt.Errorf("%s: %w", keys[i], cerr)
		}
		out[i] = vals
	}
	return out, nil
}

var (
	_ Store  = (*Redis)(nil)
	_ Source = Queue{}
)
