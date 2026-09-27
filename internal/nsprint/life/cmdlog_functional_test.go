//go:build functional

package life_test

import (
	"context"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// cmdLog is a go-redis hook that records every command name, and how many
// pipelines and single commands reached the server.
type cmdLog struct {
	mu        sync.Mutex
	names     []string
	pipelines int
	singles   int
	before    func(redis.Cmder) // runs before the first FCALL, once
}

func (l *cmdLog) DialHook(next redis.DialHook) redis.DialHook { return next }

func (l *cmdLog) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		l.mu.Lock()
		l.singles++
		l.names = append(l.names, cmd.Name())
		b := l.before
		if cmd.Name() == "fcall" {
			l.before = nil
		} else {
			b = nil
		}
		l.mu.Unlock()
		if b != nil {
			b(cmd)
		}
		return next(ctx, cmd)
	}
}

func (l *cmdLog) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		l.mu.Lock()
		l.pipelines++
		for _, c := range cmds {
			l.names = append(l.names, c.Name())
		}
		l.mu.Unlock()
		return next(ctx, cmds)
	}
}

// hookedStore is a store on the control Redis whose client carries log.
func hookedStore(t *testing.T, addr string, log *cmdLog) *store.Store {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: addr, PoolSize: 1})
	t.Cleanup(func() { _ = c.Close() })
	// One connection, opened before the hook, so its handshake is not counted.
	if err := c.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	c.AddHook(log)
	return store.New(c)
}
