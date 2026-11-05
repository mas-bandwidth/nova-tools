package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type redisStore struct{ c *redis.Client }

// quiet drops go-redis's own log lines: nova-bus reports its errors itself.
type quiet struct{}

func (quiet) Printf(context.Context, string, ...any) {}

// dialRedis keeps one connection: every command in a process runs in turn.
// The client never retries a command: a retried XADD can send a message
// twice, and a retried XREADGROUP > can strand a batch in the pending list.
func dialRedis(addr, password string) Store {
	redis.SetLogger(quiet{})
	return redisStore{redis.NewClient(&redis.Options{Addr: addr, Password: password, PoolSize: 1, MaxRetries: -1})}
}

func (r redisStore) Add(ctx context.Context, stream string, fields map[string]string) (string, error) {
	values := make(map[string]any, len(fields))
	for k, v := range fields {
		values[k] = v
	}
	return r.c.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: values}).Result()
}

// presenceKey is the key a hook holds while it runs.
func presenceKey(name string) string { return "bus:presence:" + name }

// Group creates the hook group at the start of the stream, so messages sent
// before the first hook are delivered too.
func (r redisStore) Group(ctx context.Context, stream string) error {
	err := r.c.XGroupCreateMkStream(ctx, stream, group, "0").Err()
	if err != nil && strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return nil
	}
	return err
}

func (r redisStore) Read(ctx context.Context, stream string, pending bool, block time.Duration) ([]Msg, error) {
	args := &redis.XReadGroupArgs{Group: group, Consumer: group, Streams: []string{stream, ">"}, Count: 100, Block: block}
	if pending {
		args.Streams[1], args.Block = "0", -1
	}
	res, err := r.c.XReadGroup(ctx, args).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var msgs []Msg
	for _, s := range res {
		msgs = append(msgs, toMsgs(s.Messages)...)
	}
	return msgs, nil
}

func toMsgs(xs []redis.XMessage) []Msg {
	var msgs []Msg
	for _, x := range xs {
		m := Msg{ID: x.ID, Fields: map[string]string{}}
		for k, v := range x.Values {
			m.Fields[k] = fmt.Sprint(v)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

// Ack acks the entry and deletes it from the stream in one MULTI, so an inbox
// holds only what is waiting.
func (r redisStore) Ack(ctx context.Context, stream, id string) error {
	_, err := r.c.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.XAck(ctx, stream, group, id)
		p.XDel(ctx, stream, id)
		return nil
	})
	return err
}

// Count is the group's pending count plus its lag (Redis 7); the stream's
// length when there is no group yet; a full count when the lag is unknown.
func (r redisStore) Count(ctx context.Context, stream string) (int64, error) {
	groups, err := r.c.XInfoGroups(ctx, stream).Result()
	if noSuchKey(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	for _, g := range groups {
		if g.Name == group && g.Lag >= 0 {
			return g.Pending + g.Lag, nil
		}
		if g.Name == group {
			msgs, err := r.Waiting(ctx, stream)
			return int64(len(msgs)), err
		}
	}
	return r.c.XLen(ctx, stream).Result()
}

// Waiting reads the group's handed-but-unacked entries (XPENDING, then one
// pipelined XRANGE each), then every entry after its last-delivered id, 1000
// at a time; with no group yet, the whole stream. It is O(entries waiting).
func (r redisStore) Waiting(ctx context.Context, stream string) ([]Msg, error) {
	groups, err := r.c.XInfoGroups(ctx, stream).Result()
	if noSuchKey(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var msgs []Msg
	start := "-"
	for _, g := range groups {
		if g.Name != group {
			continue
		}
		start = "(" + g.LastDeliveredID
		if g.Pending == 0 {
			break
		}
		ps, err := r.c.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: stream, Group: group, Start: "-", End: "+", Count: g.Pending}).Result()
		if err != nil {
			return nil, err
		}
		cmds, err := r.c.Pipelined(ctx, func(p redis.Pipeliner) error {
			for _, x := range ps {
				p.XRange(ctx, stream, x.ID, x.ID)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		for _, c := range cmds {
			msgs = append(msgs, toMsgs(c.(*redis.XMessageSliceCmd).Val())...)
		}
	}
	for {
		xs, err := r.c.XRangeN(ctx, stream, start, "+", 1000).Result()
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, toMsgs(xs)...)
		if len(xs) < 1000 {
			return msgs, nil
		}
		start = "(" + xs[len(xs)-1].ID
	}
}

func noSuchKey(err error) bool { return err != nil && strings.Contains(err.Error(), "no such key") }

// claim sets the key unless it holds another value; release deletes it only
// if it holds this one (tla/Presence.tla Claim, Release: StartOnlyWhenFree,
// OnlyOwnerDeletes).
var (
	claim = redis.NewScript(`local v = redis.call('GET', KEYS[1])
if v and v ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
return 1`)
	release = redis.NewScript(`if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0`)
)

func (r redisStore) Claim(ctx context.Context, name, value string, ttl time.Duration) (bool, error) {
	n, err := claim.Run(ctx, r.c, []string{presenceKey(name)}, value, ttl.Milliseconds()).Int()
	return n == 1, err
}

func (r redisStore) Release(ctx context.Context, name, value string) error {
	return release.Run(ctx, r.c, []string{presenceKey(name)}, value).Err()
}

// Presence scans bus:presence:*; a key that expires between the scan and
// the read is left out, and one that is not a string has the value "".
func (r redisStore) Presence(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	iter := r.c.Scan(ctx, 0, presenceKey("*"), 100).Iterator()
	for iter.Next(ctx) {
		name := strings.TrimPrefix(iter.Val(), presenceKey(""))
		v, err := r.c.Get(ctx, iter.Val()).Result()
		switch {
		case errors.Is(err, redis.Nil):
		case err != nil && strings.HasPrefix(err.Error(), "WRONGTYPE"):
			out[name] = ""
		case err != nil:
			return nil, err
		default:
			out[name] = v
		}
	}
	return out, iter.Err()
}
