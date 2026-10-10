//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/pkg/testredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type queueTripTrace struct {
	mu    sync.Mutex
	trips []string
}

func (h *queueTripTrace) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}
func (h *queueTripTrace) record(cmds []redis.Cmder) {
	var names []string
	for _, c := range cmds {
		name := c.Name()
		if (name == "fcall" || name == "fcall_ro") && len(c.Args()) > 1 {
			if fn, ok := c.Args()[1].(string); ok {
				name += " " + fn
			}
		}
		names = append(names, name)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.trips = append(h.trips, strings.Join(names, " + "))
}
func (h *queueTripTrace) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, c redis.Cmder) error { h.record([]redis.Cmder{c}); return next(ctx, c) }
}
func (h *queueTripTrace) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cs []redis.Cmder) error { h.record(cs); return next(ctx, cs) }
}
func (h *queueTripTrace) reset() { h.mu.Lock(); defer h.mu.Unlock(); h.trips = nil }
func (h *queueTripTrace) trace() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.trips...)
}

// Baseline for the worker-batch gate: setup writes stay local, and each read
// is a real CLI path across the latency seam, including cold connection setup.
func TestWorkerQueueReadCostAtOneHundredMilliseconds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	addr := testutil.Start(t)
	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	require.NoError(t, fn.Load(ctx, admin))
	coord := newApp(func(k string) string {
		return map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "coordinator"}[k]
	})
	t.Cleanup(coord.close)
	do := func(args ...string) {
		t.Helper()
		var out, errb bytes.Buffer
		require.Equal(t, 0, coord.run(args, &out, &errb), "%v: %s %s", args, out.String(), errb.String())
	}
	do("init", "--members", "m1:4", "--readers", "reader-a,reader-b,reader-c")
	do("add", "--stream", "s1", "--count", "4", "--brief", passingBrief("read-probe-brief"))
	do("fleet", "beat", "m1")
	do("fleet", "up", "m1")
	seed := &store.Store{B: &store.Redis{C: admin, Now: time.Now}, Actor: "coordinator"}
	dealt, err := seed.Run(ctx, dealStep(sprint.DealReq{}))
	require.NoError(t, err)
	require.Len(t, dealt.Moved, 4)
	do("take", "--as", "m1", "s1-1.w1@1", "--epoch", "0")
	do("finish", "--as", "m1", "s1-1.w1@1", "--epoch", "0", "--report", "read-probe-report")
	require.NoError(t, seed.BeatReaders(ctx))
	do("ask")
	link := testredis.FarLink(t, addr, 100*time.Millisecond)
	for _, who := range []string{"m1", "reader-a"} {
		a := newApp(func(k string) string {
			return map[string]string{"NOVA_SPRINT_REDIS": link.Addr(), "NOVA_SPRINT_ACTOR": who}[k]
		})
		t.Cleanup(a.close)
		trace := &queueTripTrace{}
		attached := false
		a.backend = func(ctx context.Context, addr string, names sprint.Names) (store.Backend, error) {
			b, err := a.redisBackend(ctx, addr, names)
			if err == nil && !attached {
				b.(*store.Redis).C.AddHook(trace)
				attached = true
			}
			return b, err
		}
		for _, temperature := range []string{"cold", "warm"} {
			trace.reset()
			before := link.Writes()
			began := time.Now()
			var out, errb bytes.Buffer
			require.Equal(t, 0, a.run([]string{"queue", "--as", who, "--json"}, &out, &errb), "%s", errb.String())
			var q struct {
				Epoch uint64      `json:"epoch"`
				Cards []queueCard `json:"cards"`
			}
			require.NoError(t, json.Unmarshal(out.Bytes(), &q))
			want := 3
			if who == "reader-a" {
				want = 1
			}
			require.Len(t, q.Cards, want)
			for _, card := range q.Cards {
				require.NotNil(t, card.Packet)
				assert.Equal(t, card.ID, card.Packet.Card)
				assert.Equal(t, who, card.Packet.As)
				assert.Equal(t, card.Primary, card.Packet.Primary)
				assert.Equal(t, q.Epoch, card.Packet.Epoch)
				assert.Equal(t, passingBrief("read-probe-brief"), card.Packet.Brief)
				if who == "reader-a" {
					assert.Equal(t, "read-probe-report", card.Packet.Report)
					assert.Equal(t, "m1", card.Packet.Worker)
				}
			}
			trips := trace.trace()
			t.Logf("QUEUE %s %s logical_after_connect=%d network_writes=%d wall=%s trace=%v", who, temperature, len(trips), link.Writes()-before, time.Since(began), trips)
		}
	}
}
