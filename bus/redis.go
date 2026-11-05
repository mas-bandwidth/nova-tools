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

func dialRedis(addr, password string) Store {
	return redisStore{redis.NewClient(&redis.Options{Addr: addr, Password: password})}
}

func (r redisStore) Add(ctx context.Context, stream string, fields map[string]string) (string, error) {
	values := make(map[string]any, len(fields))
	for k, v := range fields {
		values[k] = v
	}
	return r.c.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: values}).Result()
}

// Group creates the hook group at the start of the stream, so messages sent
// before the first hook are delivered too.
func (r redisStore) Group(ctx context.Context, stream string) error {
	err := r.c.XGroupCreateMkStream(ctx, stream, group, "0").Err()
	if err != nil && strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return nil
	}
	return err
}

func (r redisStore) Read(ctx context.Context, stream string, pending bool) ([]Msg, error) {
	args := &redis.XReadGroupArgs{Group: group, Consumer: group, Streams: []string{stream, ">"}, Count: 100, Block: 30 * time.Second}
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
		for _, x := range s.Messages {
			m := Msg{ID: x.ID, Fields: map[string]string{}}
			for k, v := range x.Values {
				m.Fields[k] = fmt.Sprint(v)
			}
			msgs = append(msgs, m)
		}
	}
	return msgs, nil
}

func (r redisStore) Ack(ctx context.Context, stream, id string) error {
	return r.c.XAck(ctx, stream, group, id).Err()
}
