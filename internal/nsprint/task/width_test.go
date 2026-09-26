//go:build functional

package task_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/redis/go-redis/v9"
)

type widthTripHook struct{ single, pipeline atomic.Int64 }

func (*widthTripHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *widthTripHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.single.Add(1)
		return next(ctx, cmd)
	}
}
func (h *widthTripHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.pipeline.Add(1)
		return next(ctx, cmds)
	}
}

func TestGetWidthOnePipeline(t *testing.T) {
	t.Parallel()

	// the store with the library: the working count is the epoch-keyed
	// cell function ns_cell_zcard, in the one pipeline (nova-tools#4238)
	_, c := wstest.Start(t)
	ctx := context.Background()

	c.HSet(ctx, "friend:f:desired", "slots", 4)
	c.ZAdd(ctx, "friend:f:cards:working", redis.Z{Member: "a"})
	c.ZAdd(ctx, "friend:f:cards:working", redis.Z{Member: "b"})
	h := &widthTripHook{}
	c.AddHook(h)
	w, err := task.GetWidth(ctx, store.New(c), "f")
	if err != nil {
		t.Fatal(err)
	}
	if w.Desired != 4 || w.Leased != 2 || w.Free != 2 {
		t.Fatalf("width=%+v", w)
	}
	if h.pipeline.Load() != 1 || h.single.Load() != 0 {
		t.Fatalf("round trips: pipeline=%d single=%d, want 1/0", h.pipeline.Load(), h.single.Load())
	}
}
