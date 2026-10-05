package bus

import (
	"context"
	"errors"
	"fmt"
	"os"
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

// CallTimeout bounds one store call that answers at once when the verb gave
// no deadline of its own (SPEC-BUS.md, the deadlines). It is the same number
// as the connection's own read bound (redisconn.ReadTimeout), so the call and
// the connection agree; a store past it is refused as not answering, because
// the host may be overloaded.
const CallTimeout = redisconn.ReadTimeout

// BlockMargin is what a read that waits (recv --forever's block) is given
// over the block it asked for: the store may hand its answer back a little
// late on a host under load, so the call's deadline is the block plus this
// (SPEC-BUS.md, the deadlines). It is the same ten seconds go-redis gives the
// connection over a blocking command, so the two agree there too.
const BlockMargin = 10 * time.Second

// Redis is the Store over one go-redis client. Each method is one round trip
// under a deadline: Timeout (CallTimeout when zero) for a call that answers
// at once, the block asked for plus the margin for a read that waits. The
// deadline is the call's context and also the connection's read and write
// bound for that call (bounded), so the bound that fires is the bound named.
// A call past it is answered with the timeout refusal, which names the
// address and never a login; a read that changes nothing is sent once more,
// and a write or a delivery is sent at most once, so a stalled store can
// duplicate nothing. One call at a time: the connection's bounds are raised
// for the call and put back after it.
type Redis struct {
	C *redis.Client
	// Timeout bounds one call that answers at once; zero is CallTimeout.
	// The tool fills it from its --timeout (SPEC-BUS.md, the deadlines).
	Timeout time.Duration
	// Margin is what a waiting read is given over its block; zero is
	// BlockMargin. A test shortens it so a stall costs no wall clock.
	Margin time.Duration
}

// timeout is the deadline of a call that answers at once: the verb's own,
// else CallTimeout.
func (r Redis) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return CallTimeout
}

// margin is what a waiting read is given over its block.
func (r Redis) margin() time.Duration {
	if r.Margin > 0 {
		return r.Margin
	}
	return BlockMargin
}

// bounded runs one store call under the deadline d, and returns its error as
// it came. go-redis puts a socket deadline of the sooner of the context and
// the client's read or write bound on every command, so the connection's
// bounds are raised to at least d for the call and put back after: a
// --timeout above the connection's 5 s is then the deadline that fires, not
// the connection's (SPEC-BUS.md, the deadlines). A bound of zero or less is
// the client's "none" and is left alone.
func (r Redis) bounded(ctx context.Context, d time.Duration, run func(context.Context) error) error {
	o := r.C.Options()
	read, write := o.ReadTimeout, o.WriteTimeout
	defer func() { o.ReadTimeout, o.WriteTimeout = read, write }()
	if read > 0 && read < d {
		o.ReadTimeout = d
	}
	if write > 0 && write < d {
		o.WriteTimeout = d
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	return run(ctx)
}

// refusal turns a deadline that ran out into the timeout refusal: one line
// that names the deadline, the address and the next thing to do. The address
// is host:port or a socket path, never a login, and the password rides no
// option of the client (internal/redisconn).
func (r Redis) refusal(d time.Duration, err error) error {
	if !timedOut(err) {
		return err
	}
	return fmt.Errorf("redis did not answer within %s at %s: the host may be overloaded (load average), try again", d, r.C.Options().Addr)
}

// call runs one store call under the deadline d, a deadline that ran out
// answered with the timeout refusal.
func (r Redis) call(ctx context.Context, d time.Duration, run func(context.Context) error) error {
	return r.refusal(d, r.bounded(ctx, d, run))
}

// read is call over a read that changes nothing (SPEC-BUS.md, the
// deadlines): one that ran past its deadline is sent once more, under a
// deadline of its own, and the second answer stands.
func (r Redis) read(ctx context.Context, run func(context.Context) error) error {
	d := r.timeout()
	if err := r.bounded(ctx, d, run); !timedOut(err) {
		return err
	}
	return r.refusal(d, r.bounded(ctx, d, run))
}

// timedOut says whether err is a deadline that ran out: the context's own or
// the connection's. A cancellation (a signal) is not one, and stays the
// caller's to answer.
func timedOut(err error) bool {
	return err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded))
}

func (r Redis) Roster(ctx context.Context) ([]string, time.Time, error) {
	var names []string
	var now time.Time
	err := r.read(ctx, func(ctx context.Context) error {
		pipe := r.C.Pipeline()
		friends := pipe.SMembers(ctx, friendsKey)
		machines := pipe.SMembers(ctx, machinesKey)
		at := pipe.Time(ctx)
		if err := redisconn.Exec(ctx, pipe); err != nil {
			return err
		}
		names, now = append(friends.Val(), machines.Val()...), at.Val()
		return nil
	})
	if err != nil {
		return nil, time.Time{}, err
	}
	return names, now, nil
}

func (r Redis) AddAll(ctx context.Context, streams []string, fields map[string]string) error {
	// a write: sent at most once, never retried (SPEC-BUS.md, the deadlines)
	return r.call(ctx, r.timeout(), func(ctx context.Context) error {
		pipe := r.C.TxPipeline()
		for _, s := range streams {
			pipe.XAdd(ctx, &redis.XAddArgs{Stream: s, ID: "*", Values: toValues(fields)})
		}
		return redisconn.Exec(ctx, pipe)
	})
}

func (r Redis) EnsureGroup(ctx context.Context, stream, group string) error {
	err := r.call(ctx, r.timeout(), func(ctx context.Context) error {
		return r.C.XGroupCreateMkStream(ctx, stream, group, "0").Err()
	})
	if err != nil && strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return nil // the group is there: what was asked for
	}
	return err
}

func (r Redis) Claim(ctx context.Context, stream, group, consumer string, minIdle time.Duration, count int) ([]Entry, error) {
	var msgs []redis.XMessage
	err := r.call(ctx, r.timeout(), func(ctx context.Context) error { // a delivery: sent at most once
		var err error
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
	// a blocking read waits its block plus the margin, never the call
	// timeout, and delivers, so it is sent at most once (SPEC-BUS.md, the
	// deadlines)
	d := r.timeout()
	if block > 0 {
		d = block + r.margin()
	}
	var res []redis.XStream
	err := r.call(ctx, d, func(ctx context.Context) error {
		args := &redis.XReadGroupArgs{Group: group, Consumer: consumer, Streams: []string{stream, ">"}, Count: int64(count), Block: -1}
		if block > 0 {
			args.Block = block
		}
		var err error
		res, err = r.C.XReadGroup(ctx, args).Result()
		if errors.Is(err, redis.Nil) {
			return nil // the block ran out with nothing: not an error
		}
		return err
	})
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
	return r.call(ctx, r.timeout(), func(ctx context.Context) error { // a delivery's undoing: sent at most once
		return r.C.Do(ctx, args...).Err()
	})
}

func (r Redis) Ack(ctx context.Context, stream, group string, ids ...string) (int64, error) {
	var n int64
	err := r.call(ctx, r.timeout(), func(ctx context.Context) error { // an ack: sent at most once
		var err error
		n, err = r.C.XAck(ctx, stream, group, ids...).Result()
		return err
	})
	return n, err
}

func (r Redis) Pending(ctx context.Context, stream, group string, count int) ([]string, error) {
	var rows []redis.XPendingExt
	err := r.read(ctx, func(ctx context.Context) error {
		var err error
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
	err := r.read(ctx, func(ctx context.Context) error {
		var err error
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
	err := r.read(ctx, func(ctx context.Context) error {
		var err error
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
	err := r.read(ctx, func(ctx context.Context) error {
		out = nil // a retry starts the answer over, never appends twice
		pipe := r.C.Pipeline()
		cmds := make([]*redis.XMessageSliceCmd, len(ids))
		for i, id := range ids {
			cmds[i] = pipe.XRange(ctx, stream, id, id)
		}
		if err := redisconn.Exec(ctx, pipe); err != nil {
			return err
		}
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
