package wake

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestWakeReachesTheWindowWithoutPolling checks that a friend window's
// heartbeat and its wake ride one RESP3 connection, and a wake reaches the
// window from the parked blocking read with no polling. The test asserts the
// events (delivery, the delivered entry, the command count around it), never
// elapsed wall time (docs/SPEC-CI.md CI-WAITS): a wake delivered by the one
// parked XREAD, with the beat interval at 5 s, is the no-polling latency bound.
func TestWakeReachesTheWindowWithoutPolling(t *testing.T) {
	mr := miniredis.RunT(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const presence, stream = "friend:stella:beat", "wake:stella"
	pool, err := NewResp3ConnPool(ctx, mr.Addr(), presence, stream, 30*time.Second)
	if err != nil {
		t.Fatalf("NewResp3ConnPool: %v", err)
	}
	defer pool.Close()

	// An entry already on the stream before Subscribe must not wake the window.
	pub := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer pub.Close()
	if err := pub.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"kind": "stale"}}).Err(); err != nil {
		t.Fatalf("seed XADD: %v", err)
	}
	if err := pool.Subscribe(ctx); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	got := make(chan redis.XMessage, 4)
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		// Beat interval far above the wake window: a wake inside 1 s can only
		// come from the blocking read, not from a timed re-check.
		done <- pool.Run(runCtx, 5*time.Second, func(m redis.XMessage) {
			got <- m
		})
	}()

	// Heartbeat landed on the shared connection.
	deadline := time.Now().Add(30 * time.Second)
	for !mr.Exists(presence) {
		if time.Now().After(deadline) {
			t.Fatal("presence key never written")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ttl := mr.TTL(presence); ttl <= 0 || ttl > 30*time.Second {
		t.Fatalf("presence ttl = %s, want (0, 30s]", ttl)
	}

	conns := mr.TotalConnectionCount()
	cmdsBefore := mr.CommandCount()

	if err := pub.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"kind": "wake"}}).Err(); err != nil {
		t.Fatalf("XADD: %v", err)
	}
	wait := 30 * time.Second
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			wait = d
		}
	}
	// The event, not the clock: the wake is delivered, and it is the
	// post-Subscribe entry. The environment-bounded wait only stops a hung test.
	select {
	case m := <-got:
		if m.Values["kind"] != "wake" {
			t.Fatalf("delivered %v, want the post-Subscribe wake (stale entry leaked)", m.Values)
		}
	case <-time.After(wait):
		t.Fatal("wake never reached the window")
	}

	// No polling: between park and delivery the window issued at most its
	// one blocking XREAD plus the next beat and re-park (publisher XADD aside).
	if n := mr.CommandCount() - cmdsBefore - 1; n > 3 {
		t.Fatalf("window issued %d commands around one wake; a poller is running", n)
	}
	// One connection: the pool opened no new connection to deliver the wake.
	if extra := mr.TotalConnectionCount() - conns; extra > 1 { // publisher may dial once
		t.Fatalf("%d new connections during the wake; heartbeat and wake must share one", extra)
	}

	stop()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
