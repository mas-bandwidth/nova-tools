package capacity_test

import (
	"context"
	"sync/atomic"
	"testing"

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
