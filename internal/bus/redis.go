package bus

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/redis/go-redis/v9"
)

// The sets nova-config's apply fills with the names of its friend and machine
// rows (internal/config/redis.go: FriendsKey, MachinesKey), read here by name
// so this package depends on no config code.
const (
	friendsKey  = "friends"
	machinesKey = "machines"
)

// The deadlines (SPEC-BUS.md, the deadlines). CallTimeout bounds every call
// that does not block; a blocking read gets the time it asked for and
// BlockMargin more. One retry, and only for a read that changes nothing.
const (
	CallTimeout = 5 * time.Second
	BlockMargin = 10 * time.Second
)

// Redis is the Store over one go-redis client. Each method is one round trip,
// under a deadline of Timeout (CallTimeout when zero); the client must honour
// a context's deadline (redisconn sets ContextTimeoutEnabled).
type Redis struct {
	C       *redis.Client
	Timeout time.Duration
	Margin  time.Duration // a blocking read's wait beyond its block (BlockMargin when zero)
}

// TimeoutError is a call the store did not answer within After. It names the
// address and never the login.
type TimeoutError struct {
	Addr  string
	After time.Duration
	cause error
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("redis did not answer within %s at %s: the host may be overloaded (load average), try again", e.After, e.Addr)
}
func (e *TimeoutError) Unwrap() error   { return e.cause }
func (e *TimeoutError) Timeout() bool   { return true }
func (e *TimeoutError) Temporary() bool { return true }

// call runs fn under the deadline: Timeout plus extra (a blocking read's own
// wait). A deadline that runs out is a *TimeoutError. With retry set, the
// call is made a second time, once, when the first ran out of time and the
// caller's own context is still live; a send, an ack and a read that hands
// out entries never set it, since a second try could act twice.
func (r Redis) call(ctx context.Context, retry bool, extra time.Duration, fn func(ctx context.Context) error) error {
	d := r.Timeout
	if d <= 0 {
		d = CallTimeout
	}
	d += extra
	for attempt := 0; ; attempt++ {
		cctx, cancel := context.WithTimeout(ctx, d)
		err := fn(cctx)
		cancel()
		var netErr net.Error
		if err == nil || ctx.Err() != nil || !(errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout()) {
			return err
		}
		if retry && attempt == 0 {
			continue
		}
		return &TimeoutError{Addr: r.C.Options().Addr, After: d, cause: err}
	}
}

func (r Redis) Roster(ctx context.Context) ([]string, time.Time, error) {
	friends, machines, now, err := r.Members(ctx)
	return append(friends, machines...), now, err
}

func (r Redis) Members(ctx context.Context) (friends, machines []string, now time.Time, err error) {
	err = r.call(ctx, true, 0, func(ctx context.Context) error {
		pipe := r.C.Pipeline()
		f := pipe.SMembers(ctx, friendsKey)
		m := pipe.SMembers(ctx, machinesKey)
		t := pipe.Time(ctx)
		if err := redisconn.Exec(ctx, pipe); err != nil {
			return err
		}
		friends, machines, now = f.Val(), m.Val(), t.Val()
		return nil
	})
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	return friends, machines, now, nil
}

func (r Redis) AddAll(ctx context.Context, streams []string, fields map[string]string, marks ...Mark) error {
	return r.call(ctx, false, 0, func(ctx context.Context) error {
		pipe := r.C.TxPipeline()
		for _, s := range streams {
			pipe.XAdd(ctx, &redis.XAddArgs{Stream: s, ID: "*", Values: toValues(fields)})
		}
		for _, m := range marks {
			if m.Clear {
				pipe.HDel(ctx, m.Key, m.Field)
			} else {
				pipe.HSet(ctx, m.Key, m.Field, m.Value)
			}
		}
		return redisconn.Exec(ctx, pipe)
	})
}

func (r Redis) Unmark(ctx context.Context, key string, fields ...string) (n int64, err error) {
	err = r.call(ctx, false, 0, func(ctx context.Context) (err error) {
		n, err = r.C.HDel(ctx, key, fields...).Result()
		return err
	})
	return n, err
}

func (r Redis) Marks(ctx context.Context, keys ...string) ([]map[string]string, error) {
	var out []map[string]string
	err := r.call(ctx, true, 0, func(ctx context.Context) error {
		pipe := r.C.Pipeline()
		cmds := make([]*redis.MapStringStringCmd, len(keys))
		for i, k := range keys {
			cmds[i] = pipe.HGetAll(ctx, k)
		}
		if err := redisconn.Exec(ctx, pipe); err != nil {
			return err
		}
		out = make([]map[string]string, len(keys))
		for i, c := range cmds {
			out[i] = c.Val()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r Redis) EnsureGroup(ctx context.Context, stream, group string) error {
	return r.call(ctx, false, 0, func(ctx context.Context) error {
		err := r.C.XGroupCreateMkStream(ctx, stream, group, "0").Err()
		if err != nil && strings.HasPrefix(err.Error(), "BUSYGROUP") {
			return nil // the group is there: what was asked for
		}
		return err
	})
}

func (r Redis) Claim(ctx context.Context, stream, group, consumer string, minIdle time.Duration, count int) ([]Entry, error) {
	var msgs []redis.XMessage
	err := r.call(ctx, false, 0, func(ctx context.Context) (err error) {
		msgs, _, err = r.C.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream: stream, Group: group, Consumer: consumer, MinIdle: minIdle, Start: "0-0", Count: int64(count),
		}).Result()
		return err
	})
	if err != nil {
		return nil, err
	}
	return entries(stream, msgs), nil
}

func (r Redis) Read(ctx context.Context, stream, group, consumer string, block time.Duration, count int) ([]Entry, error) {
	args := &redis.XReadGroupArgs{Group: group, Consumer: consumer, Streams: []string{stream, ">"}, Count: int64(count), Block: -1}
	var extra time.Duration
	if block > 0 {
		args.Block = block
		extra = block + r.Margin
		if r.Margin <= 0 {
			extra = block + BlockMargin
		}
	}
	var res []redis.XStream
	err := r.call(ctx, false, extra, func(ctx context.Context) (err error) {
		res, err = r.C.XReadGroup(ctx, args).Result()
		return err
	})
	if errors.Is(err, redis.Nil) {
		return nil, nil // the block ran out with nothing: not an error
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, s := range res {
		out = append(out, entries(s.Stream, s.Messages)...)
	}
	return out, nil
}

func (r Redis) Release(ctx context.Context, stream, group string, ids ...string) error {
	args := []any{"XCLAIM", stream, group, Consumer, 0}
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, "IDLE", ClaimAfter.Milliseconds(), "JUSTID")
	return r.call(ctx, false, 0, func(ctx context.Context) error { return r.C.Do(ctx, args...).Err() })
}

func (r Redis) Ack(ctx context.Context, stream, group string, ids ...string) (n int64, err error) {
	err = r.call(ctx, false, 0, func(ctx context.Context) (err error) {
		n, err = r.C.XAck(ctx, stream, group, ids...).Result()
		return err
	})
	return n, err
}

func (r Redis) Pending(ctx context.Context, stream, group string, count int) ([]string, error) {
	var rows []redis.XPendingExt
	err := r.call(ctx, true, 0, func(ctx context.Context) (err error) {
		rows, err = r.C.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: stream, Group: group, Start: "-", End: "+", Count: int64(count)}).Result()
		return err
	})
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(rows))
	for i, p := range rows {
		ids[i] = p.ID
	}
	return ids, nil
}

func (r Redis) Group(ctx context.Context, stream, group string) (string, bool, error) {
	var groups []redis.XInfoGroup
	err := r.call(ctx, true, 0, func(ctx context.Context) (err error) {
		groups, err = r.C.XInfoGroups(ctx, stream).Result()
		return err
	})
	if err != nil {
		if strings.HasPrefix(err.Error(), "ERR no such key") {
			return "", false, nil
		}
		return "", false, err
	}
	for _, g := range groups {
		if g.Name == group {
			return g.LastDeliveredID, true, nil
		}
	}
	return "", false, nil
}

func (r Redis) Range(ctx context.Context, stream, from, to string, count int) ([]Entry, error) {
	var msgs []redis.XMessage
	err := r.call(ctx, true, 0, func(ctx context.Context) (err error) {
		if count > 0 {
			msgs, err = r.C.XRangeN(ctx, stream, from, to, int64(count)).Result()
		} else {
			msgs, err = r.C.XRange(ctx, stream, from, to).Result()
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return entries(stream, msgs), nil
}

func (r Redis) Get(ctx context.Context, stream string, ids []string) ([]Entry, error) {
	var out []Entry
	err := r.call(ctx, true, 0, func(ctx context.Context) error {
		pipe := r.C.Pipeline()
		cmds := make([]*redis.XMessageSliceCmd, len(ids))
		for i, id := range ids {
			cmds[i] = pipe.XRange(ctx, stream, id, id)
		}
		if err := redisconn.Exec(ctx, pipe); err != nil {
			return err
		}
		out = nil
		for _, c := range cmds {
			out = append(out, entries(stream, c.Val())...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func toValues(fields map[string]string) map[string]any {
	v := make(map[string]any, len(fields))
	for k, s := range fields {
		v[k] = s
	}
	return v
}

func entries(stream string, msgs []redis.XMessage) []Entry {
	out := make([]Entry, 0, len(msgs))
	for _, m := range msgs {
		fields := make(map[string]string, len(m.Values))
		for k, v := range m.Values {
			fields[k] = fmt.Sprint(v)
		}
		out = append(out, Entry{Stream: stream, Entry: m.ID, Fields: fields})
	}
	return out
}
