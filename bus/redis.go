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
func dialRedis(addr, user, password string) Store {
	redis.SetLogger(quiet{})
	return redisStore{redis.NewClient(redisOptions(addr, user, password))}
}

// redisOptions AUTHs as user when one is given, else as Redis's default user.
func redisOptions(addr, user, password string) *redis.Options {
	return &redis.Options{Addr: addr, Username: user, Password: password, PoolSize: 1, MaxRetries: -1}
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

// Hooked is whether the stream has its hook group: XINFO GROUPS on a stream
// that does not exist is an error naming no such key.
func (r redisStore) Hooked(ctx context.Context, stream string) (bool, error) {
	groups, err := r.c.XInfoGroups(ctx, stream).Result()
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "no such key") {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, g := range groups {
		if g.Name == group {
			return true, nil
		}
	}
	return false, nil
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
func (r redisStore) Ack(ctx context.Context, stream, id string) (int64, error) {
	var acked *redis.IntCmd
	_, err := r.c.TxPipelined(ctx, func(p redis.Pipeliner) error {
		acked = p.XAck(ctx, stream, group, id)
		p.XDel(ctx, stream, id)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return acked.Val(), nil
}

// Count is one XLEN: an ack deletes its entry, so a stream holds exactly
// the entries no hook has acked.
func (r redisStore) Count(ctx context.Context, stream string) (int64, error) {
	return r.c.XLen(ctx, stream).Result()
}

// Waiting reads the whole stream, 1000 entries at a time.
func (r redisStore) Waiting(ctx context.Context, stream string) ([]Msg, error) {
	var msgs []Msg
	for start := "-"; ; {
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

// Trim deletes every entry older than id (XTRIM MINID).
func (r redisStore) Trim(ctx context.Context, stream, id string) error {
	return r.c.XTrimMinID(ctx, stream, id).Err()
}

// claim sets the key unless it holds another value; release deletes it only
// if it holds this one (tla/Presence.tla Claim, Release: StartOnlyWhenFree,
// OnlyOwnerDeletes).
var (
	claim = redis.NewScript(`local v = redis.call('GET', KEYS[1])
if v and v ~= ARGV[1] then return v end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
return ''`)
	release = redis.NewScript(`if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0`)
)

func (r redisStore) Claim(ctx context.Context, name, value string, ttl time.Duration) (string, error) {
	return claim.Run(ctx, r.c, []string{presenceKey(name)}, value, ttl.Milliseconds()).Text()
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
