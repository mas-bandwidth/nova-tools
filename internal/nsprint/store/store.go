// Package store provides the Redis client shared by nova-sprint verbs.
package store

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Store owns a Redis connection. Mutating verbs use FCALL through Client;
// batches of independent reads use PipelineHMGet.
type Store struct {
	client *redis.Client
}

func Open(ctx context.Context, addr string) (*Store, error) {
	if addr == "" {
		return nil, fmt.Errorf("redis address is required")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis at %s: %w", addr, err)
	}
	return &Store{client: client}, nil
}

func New(client *redis.Client) *Store { return &Store{client: client} }

func (s *Store) Client() *redis.Client { return s.client }

func (s *Store) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

// HashRead names one HMGET without issuing it yet.
type HashRead struct {
	Key    string
	Fields []string
}

// PipelineHMGet queues every read before Exec. Redis receives the full batch
// before reading any reply and returns replies in the same order. The buffered
// transport may split the batch into multiple writes, but it uses one exchange.
func (s *Store) PipelineHMGet(ctx context.Context, reads []HashRead) ([][]any, error) {
	if len(reads) == 0 {
		return [][]any{}, nil
	}
	pipe := s.client.Pipeline()
	cmds := make([]*redis.SliceCmd, len(reads))
	for i, read := range reads {
		if read.Key == "" || len(read.Fields) == 0 {
			return nil, fmt.Errorf("read %d needs a key and fields", i)
		}
		cmds[i] = pipe.HMGet(ctx, read.Key, read.Fields...)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("pipeline HMGET: %w", err)
	}
	out := make([][]any, len(cmds))
	for i, cmd := range cmds {
		values, err := cmd.Result()
		if err != nil {
			return nil, fmt.Errorf("HMGET %d: %w", i, err)
		}
		out[i] = values
	}
	return out, nil
}
