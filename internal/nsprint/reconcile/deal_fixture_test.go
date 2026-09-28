//go:build functional

package reconcile_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// The friend deal duty (nova-tools #3873) on a throwaway redis-server.

type dealFixture struct {
	ctx context.Context
	c   *redis.Client
	l   *reconcile.Lease
}

func newDealFixture(t *testing.T) *dealFixture {
	t.Helper()
	t.Setenv(testutil.CIEnv, "1")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load fn: %v", err)
	}
	l, err := reconcile.Acquire(ctx, store.New(c), reconcile.AcquireOptions{Host: "ctl-host"})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	t.Cleanup(func() { _ = l.Release(ctx) })
	return &dealFixture{ctx: ctx, c: c, l: l}
}

// friend registers f with slots and working ids; live writes a beat now.
func (f *dealFixture) friend(t *testing.T, name string, slots int, live bool, working ...string) {
	t.Helper()
	pipe := f.c.TxPipeline()
	pipe.SAdd(f.ctx, "friends", name)
	pipe.Set(f.ctx, "friend:"+name+":slots", slots, 0)
	if live {
		pipe.HSet(f.ctx, "friend:"+name, "at", time.Now().UTC().Format(time.RFC3339))
	}
	for i, id := range working {
		pipe.ZAdd(f.ctx, "friend:"+name+":cards:working", redis.Z{Score: float64(i + 1), Member: id})
	}
	if _, err := pipe.Exec(f.ctx); err != nil {
		t.Fatal(err)
	}
}

// cardEpoch is the fixtures' created_at origin: a real epoch-ms value, so
// the one move (#3778), which reads a created_at under 1e11 as seconds,
// keeps each card's age as its score.
const cardEpoch int64 = 1_758_800_000_000

// ready writes one ready task on stream with age (created_at ms after
// cardEpoch) and extra fields.
func (f *dealFixture) ready(t *testing.T, id, stream string, age int64, extra ...any) {
	t.Helper()
	age += cardEpoch
	pipe := f.c.TxPipeline()
	pipe.SAdd(f.ctx, "ws:names", stream)
	pipe.HSet(f.ctx, "task:"+id, append([]any{"stream", stream, "state", "ready", "created_at", age, "kind", "build"}, extra...)...)
	pipe.ZAdd(f.ctx, "ws:"+stream+":ready", redis.Z{Score: float64(age), Member: id})
	if _, err := pipe.Exec(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func (f *dealFixture) zcard(t *testing.T, key string) int64 {
	t.Helper()
	n, err := f.c.ZCard(f.ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *dealFixture) members(t *testing.T, key string) []string {
	t.Helper()
	m, err := f.c.ZRange(f.ctx, key, 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// stubDealFriend reloads the library with ns_deal_friend replaced by a stub
// that takes nothing and refuses the ids it is handed with the given whys,
// in order (Lua body returning the reply table; args[5] is the first id).
func (f *dealFixture) stubDealFriend(t *testing.T, reply string) {
	t.Helper()
	src, err := fn.Source()
	if err != nil {
		t.Fatal(err)
	}
	const orig = "redis.register_function('ns_deal_friend', deal_friend)"
	if !strings.Contains(src, orig) {
		t.Fatalf("library source has no %q to stub", orig)
	}
	src = strings.Replace(src, orig, "redis.register_function('ns_deal_friend', function(keys, args) return "+reply+" end)", 1)
	must(t, f.c.FunctionLoadReplace(f.ctx, src).Err())
}
