package bus

import (
	"context"
	"errors"
	"fmt"
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

// Redis is the Store over one go-redis client. Each method is one round trip.
type Redis struct{ C *redis.Client }

func (r Redis) Roster(ctx context.Context) ([]string, time.Time, error) {
	friends, machines, now, err := r.Members(ctx)
	return append(friends, machines...), now, err
}

func (r Redis) Members(ctx context.Context) ([]string, []string, time.Time, error) {
	pipe := r.C.Pipeline()
	friends := pipe.SMembers(ctx, friendsKey)
	machines := pipe.SMembers(ctx, machinesKey)
	now := pipe.Time(ctx)
	if err := redisconn.Exec(ctx, pipe); err != nil {
		return nil, nil, time.Time{}, err
	}
	return friends.Val(), machines.Val(), now.Val(), nil
}

func (r Redis) AddAll(ctx context.Context, streams []string, fields map[string]string, marks ...Mark) error {
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
}

func (r Redis) Unmark(ctx context.Context, key string, fields ...string) (int64, error) {
	return r.C.HDel(ctx, key, fields...).Result()
}

func (r Redis) Marks(ctx context.Context, keys ...string) ([]map[string]string, error) {
	pipe := r.C.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(keys))
	for i, k := range keys {
		cmds[i] = pipe.HGetAll(ctx, k)
	}
	if err := redisconn.Exec(ctx, pipe); err != nil {
		return nil, err
	}
	out := make([]map[string]string, len(keys))
	for i, c := range cmds {
		out[i] = c.Val()
	}
	return out, nil
}

func (r Redis) SetMarks(ctx context.Context, key string, fields map[string]string) error {
	if len(fields) == 0 {
		return nil
	}
	vals := make([]any, 0, len(fields)*2)
	for k, v := range fields {
		vals = append(vals, k, v)
	}
	return r.C.HSet(ctx, key, vals...).Err()
}

func (r Redis) Time(ctx context.Context) (time.Time, error) {
	return r.C.Time(ctx).Result()
}

func (r Redis) EnsureGroup(ctx context.Context, stream, group string) error {
	err := r.C.XGroupCreateMkStream(ctx, stream, group, "0").Err()
	if err != nil && strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return nil // the group is there: what was asked for
	}
	return err
}

func (r Redis) Claim(ctx context.Context, stream, group, consumer string, minIdle time.Duration, count int) ([]Entry, error) {
	msgs, _, err := r.C.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream: stream, Group: group, Consumer: consumer, MinIdle: minIdle, Start: "0-0", Count: int64(count),
	}).Result()
	if err != nil {
		return nil, err
	}
	return entries(stream, msgs), nil
}

func (r Redis) Read(ctx context.Context, stream, group, consumer string, block time.Duration, count int) ([]Entry, error) {
	args := &redis.XReadGroupArgs{Group: group, Consumer: consumer, Streams: []string{stream, ">"}, Count: int64(count), Block: -1}
	if block > 0 {
		args.Block = block
	}
	res, err := r.C.XReadGroup(ctx, args).Result()
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
	return r.C.Do(ctx, args...).Err()
}

func (r Redis) Ack(ctx context.Context, stream, group string, ids ...string) (int64, error) {
	return r.C.XAck(ctx, stream, group, ids...).Result()
}

func (r Redis) Pending(ctx context.Context, stream, group string, count int) ([]string, error) {
	rows, err := r.C.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: stream, Group: group, Start: "-", End: "+", Count: int64(count)}).Result()
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
	groups, err := r.C.XInfoGroups(ctx, stream).Result()
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
	var err error
	if count > 0 {
		msgs, err = r.C.XRangeN(ctx, stream, from, to, int64(count)).Result()
	} else {
		msgs, err = r.C.XRange(ctx, stream, from, to).Result()
	}
	if err != nil {
		return nil, err
	}
	return entries(stream, msgs), nil
}

func (r Redis) Get(ctx context.Context, stream string, ids []string) ([]Entry, error) {
	pipe := r.C.Pipeline()
	cmds := make([]*redis.XMessageSliceCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.XRange(ctx, stream, id, id)
	}
	if err := redisconn.Exec(ctx, pipe); err != nil {
		return nil, err
	}
	var out []Entry
	for _, c := range cmds {
		out = append(out, entries(stream, c.Val())...)
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
