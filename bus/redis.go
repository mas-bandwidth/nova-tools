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

// dialRedis keeps one connection: every command in a process runs in turn.
func dialRedis(addr, password string) Store {
	return redisStore{redis.NewClient(&redis.Options{Addr: addr, Password: password, PoolSize: 1})}
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

func (r redisStore) Ack(ctx context.Context, stream, id string) error {
	return r.c.XAck(ctx, stream, group, id).Err()
}

// Waiting reads the group's handed-but-unacked entries (XPENDING), then every
// entry after its last-delivered id; with no group yet, the whole stream.
func (r redisStore) Waiting(ctx context.Context, stream string) ([]Msg, error) {
	if n, err := r.c.Exists(ctx, stream).Result(); err != nil || n == 0 {
		return nil, err
	}
	groups, err := r.c.XInfoGroups(ctx, stream).Result()
	if err != nil {
		return nil, err
	}
	var msgs []Msg
	last := "0-0"
	for _, g := range groups {
		if g.Name != group {
			continue
		}
		last = g.LastDeliveredID
		if g.Pending == 0 {
			break
		}
		ps, err := r.c.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: stream, Group: group, Start: "-", End: "+", Count: g.Pending}).Result()
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			xs, err := r.c.XRange(ctx, stream, p.ID, p.ID).Result()
			if err != nil {
				return nil, err
			}
			msgs = append(msgs, toMsgs(xs)...)
		}
	}
	xs, err := r.c.XRange(ctx, stream, "("+last, "+").Result()
	return append(msgs, toMsgs(xs)...), err
}

func (r redisStore) SetPresence(ctx context.Context, name, value string, ttl time.Duration) error {
	return r.c.Set(ctx, presenceKey(name), value, ttl).Err()
}

func (r redisStore) DelPresence(ctx context.Context, name string) error {
	return r.c.Del(ctx, presenceKey(name)).Err()
}

// Presence scans bus:presence:*; a key that expires between the scan and
// the read is left out.
func (r redisStore) Presence(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	iter := r.c.Scan(ctx, 0, presenceKey("*"), 100).Iterator()
	for iter.Next(ctx) {
		v, err := r.c.Get(ctx, iter.Val()).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[strings.TrimPrefix(iter.Val(), presenceKey(""))] = v
	}
	return out, iter.Err()
}
