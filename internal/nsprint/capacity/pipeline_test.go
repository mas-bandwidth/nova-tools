package capacity_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

type tripHook struct{ single, pipeline atomic.Int64 }

func (*tripHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *tripHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.single.Add(1)
		return next(ctx, cmd)
	}
}
func (h *tripHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.pipeline.Add(1)
		return next(ctx, cmds)
	}
}

func TestConsumersOnePipeline(t *testing.T) {
	_, seed := redisControl(t)
	ctx := context.Background()
	seed.SAdd(ctx, "friends", "a", "b")
	seed.SAdd(ctx, "benches", "runner")
	seed.HSet(ctx, "friend:a:desired", "slots", 3, "machine", "m")
	seed.HSet(ctx, "friend:b:desired", "slots", 4, "machine", "m")
	seed.HSet(ctx, "bench:runner:desired", "slots", 5, "machine", "m")

	h := &tripHook{}
	seed.AddHook(h)
	got, err := (capacity.RedisReader{Store: store.New(seed)}).Consumers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("consumers=%v", got)
	}
	if h.pipeline.Load() != 1 || h.single.Load() != 0 {
		t.Fatalf("round trips: pipeline=%d single=%d, want 1/0", h.pipeline.Load(), h.single.Load())
	}
}

// TestCapacityFourFriendsSixBenchesOnePipeline is the #3265 DONE-WHEN: with 4
// friends and 6 benches registered, the ceiling guard reads the machine
// ceiling and every consumer in one pipeline (it was 2 + F + B = 12 serial
// reads plus the ceiling), and a desired write is that pipeline plus one
// FCALL. store.Trips, the counter the verb prints as trips=<n>, agrees.
func TestCapacityFourFriendsSixBenchesOnePipeline(t *testing.T) {
	st, seed := redisControl(t)
	ctx := context.Background()
	if _, err := capacity.SetMachine(ctx, st, "m", 100, 0, 0, "test", ""); err != nil {
		t.Fatal(err)
	}
	for i, f := range []string{"f1", "f2", "f3", "f4"} {
		seed.SAdd(ctx, "friends", f)
		seed.HSet(ctx, "friend:"+f+":desired", "slots", i+1, "machine", "m")
	}
	for i, b := range []string{"b1", "b2", "b3", "b4", "b5", "b6"} {
		seed.SAdd(ctx, "benches", b)
		seed.HSet(ctx, "bench:"+b+":desired", "slots", i+1, "machine", "m")
	}

	h := &tripHook{}
	seed.AddHook(h)
	start := time.Now()
	plan, err := capacity.Evaluate(ctx, capacity.RedisReader{Store: st}, "m", capacity.KindFriend, "f1", 5)
	if err != nil {
		t.Fatal(err)
	}
	// 2+3+4 + 1+2+3+4+5+6 = 30 for the others, 5 for f1.
	if !plan.Allowed || plan.Sum != 35 || plan.Ceiling != 100 {
		t.Fatalf("plan=%+v, want allowed 35/100", plan)
	}
	t.Logf("capacity guard, 4 friends 6 benches: pipeline=%d single=%d in %s", h.pipeline.Load(), h.single.Load(), time.Since(start))
	if h.pipeline.Load() != 1 || h.single.Load() != 0 {
		t.Fatalf("guard round trips: pipeline=%d single=%d, want 1/0", h.pipeline.Load(), h.single.Load())
	}

	trips := st.CountTrips()
	start = time.Now()
	res, err := capacity.SetFriend(ctx, st, "f1", "m", 5, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("capacity friend write: %d round trips in %s (%+v)", trips.N(), time.Since(start), res)
	if trips.N() != 2 {
		t.Fatalf("desired write trips=%d, want 2 (one pipeline, one FCALL)", trips.N())
	}
	if _, ok, consumers, err := (capacity.RedisReader{Store: st}).Snapshot(ctx, "absent"); err != nil || ok || len(consumers) != 10 {
		t.Fatalf("absent machine snapshot: ok=%v consumers=%d err=%v, want no ceiling and 10 consumers", ok, len(consumers), err)
	}
	if _, err := capacity.Evaluate(ctx, capacity.RedisReader{Store: st}, "absent", capacity.KindFriend, "f1", 1); err == nil ||
		!strings.Contains(err.Error(), "has no ceiling") {
		t.Fatalf("absent machine: %v, want no ceiling", err)
	}
}
