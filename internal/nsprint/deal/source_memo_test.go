package deal

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// roundCounter counts the client's round trips: one per command sent alone,
// one per pipeline.
type roundCounter struct{ n atomic.Int32 }

func (h *roundCounter) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *roundCounter) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.n.Add(1)
		return next(ctx, cmd)
	}
}

func (h *roundCounter) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.n.Add(1)
		return next(ctx, cmds)
	}
}

// TestReadMemoOneRoundTrip is nova-tools #3831 for the deal read: with a
// memo, a read over a store whose names did not change is ONE round trip
// and reads what a fresh read reads; a name that changed (a card pooled, a
// dependency named, a bench registered) is read again, and the read still
// equals a fresh one.
func TestReadMemoOneRoundTrip(t *testing.T) {
	c := throwawayRedis(t)
	ctx := context.Background()
	c.SAdd(ctx, "benches", "ctl-a")
	c.HSet(ctx, "bench:ctl-a:desired", "slots", "4")
	c.HSet(ctx, "bench:ctl-a:beat", "host", "ctl-a.tail", "user", "bench")
	c.HSet(ctx, "bench:ctl-a:state", "state", "UP", "at", "1")
	c.SAdd(ctx, "sprints", "control-m")
	c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: "control-m"})
	c.HSet(ctx, "s:control-m", "status", "open")
	c.HSet(ctx, "s:control-m:policy", "share", "1")
	c.ZAdd(ctx, "s:control-m:pool", redis.Z{Score: 1, Member: "one"})
	c.HSet(ctx, "s:control-m:card:one", "state", "queued", "leg", "go", "depends_on", "two")
	c.HSet(ctx, "s:control-m:card:two", "state", "ended", "outcome", "ok")

	rounds := &roundCounter{}
	mc := redis.NewClient(&redis.Options{Addr: c.Options().Addr})
	t.Cleanup(func() { _ = mc.Close() })
	if err := mc.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	mc.AddHook(rounds)
	memo := &ReadMemo{}
	read := func(what string, wantRounds int32) {
		t.Helper()
		rounds.n.Store(0)
		got, err := RedisSource{Client: mc, Memo: memo}.Read(ctx)
		if err != nil {
			t.Fatalf("%s: memo read: %v", what, err)
		}
		n := rounds.n.Load()
		want, err := RedisSource{Client: c}.Read(ctx)
		if err != nil {
			t.Fatalf("%s: fresh read: %v", what, err)
		}
		got.Now, want.Now = time.Time{}, time.Time{}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: memo read\n%+v\nfresh read\n%+v", what, got, want)
		}
		if wantRounds > 0 && n != wantRounds {
			t.Fatalf("%s: %d round trips, want %d", what, n, wantRounds)
		}
	}
	read("first read, empty memo", 0)
	read("names unchanged", 1)
	read("names unchanged again", 1)

	c.ZAdd(ctx, "s:control-m:pool", redis.Z{Score: 2, Member: "three"})
	c.HSet(ctx, "s:control-m:card:three", "state", "queued", "depends_on", "four")
	read("a card pooled naming a new dependency", 3)
	read("names unchanged after the pool moved", 1)

	c.HSet(ctx, "s:control-m:card:one", "leg", "lua")
	c.HSet(ctx, "bench:ctl-a:desired", "slots", "8")
	read("values changed, names not", 1)

	c.SAdd(ctx, "benches", "ctl-b")
	c.HSet(ctx, "bench:ctl-b:desired", "slots", "2")
	read("a bench registered", 2)
}
