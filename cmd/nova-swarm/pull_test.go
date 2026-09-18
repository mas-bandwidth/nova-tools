package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/redisq"
	"github.com/redis/go-redis/v9"
)

func testReadyGroup(t *testing.T) (*miniredis.Miniredis, redisq.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	c, err := redisq.Open(mr.Addr())
	if err != nil {
		t.Fatalf("open redis: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.EnsureGroup(context.Background(), redisq.ReadyStream, redisq.Group, "0"); err != nil {
		t.Fatalf("ensure group: %v", err)
	}
	// The real loop blocks for 30 s on an empty stream; a test must not wait
	// wall-clock for an event that a fake can deliver at once.
	old := pullBlock
	pullBlock = 50 * time.Millisecond
	t.Cleanup(func() { pullBlock = old })
	return mr, c
}

func seedReady(t *testing.T, c redisq.Client, id, label, body string) {
	t.Helper()
	if _, err := c.Add(context.Background(), redisq.ReadyStream, map[string]string{
		"id": id, "label": label, "body": body, "priority": "0",
		"needs": "-", "pushed-at": time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("seed ready: %v", err)
	}
}

func rawRedis(t *testing.T, addr string) *redis.Client {
	t.Helper()
	r := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { r.Close() })
	return r
}

func readyPending(t *testing.T, addr string) int64 {
	t.Helper()
	p, err := rawRedis(t, addr).XPending(context.Background(), redisq.ReadyStream, redisq.Group).Result()
	if err != nil {
		t.Fatalf("xpending: %v", err)
	}
	return p.Count
}

func useRunner(t *testing.T, fn pullRunner) {
	t.Helper()
	old := executePull
	executePull = fn
	t.Cleanup(func() { executePull = old })
}

// TestPullAcksOnlyAfterTheRun is the ack rule: the ready entry stays pending
// for the whole run and is acked only when the runner comes back, and the
// completion lands on cards:done with the card's own line.
func TestPullAcksOnlyAfterTheRun(t *testing.T) {
	mr, c := testReadyGroup(t)
	addr := mr.Addr()
	seedReady(t, c, "id1", "one", "RESULT: CARD-1 green\n\nrun it\n")
	slot := t.TempDir()

	ran := false
	useRunner(t, func(ctx context.Context, run cardRun, stdout, stderr io.Writer) (int, string, error) {
		ran = true
		if got := readyPending(t, addr); got != 1 {
			t.Errorf("the ready entry was acked before the run: pending=%d, want 1", got)
		}
		body, err := os.ReadFile(run.CardPath)
		if err != nil {
			t.Fatalf("the card was not written before the run: %v", err)
		}
		if !strings.Contains(string(body), "run it") {
			t.Errorf("card body = %q, want the pushed text", body)
		}
		return 0, "RESULT: CARD-1 green", nil
	})

	var errb bytes.Buffer
	code := run([]string{"pull", "--redis", addr, "--bench", "bench-a", "--slot-root", slot, "--once"},
		strings.NewReader(""), io.Discard, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("pull exit %d: %s", code, errb.String())
	}
	if !ran {
		t.Fatal("the runner never ran")
	}
	if got := readyPending(t, addr); got != 0 {
		t.Errorf("pending after the run = %d, want 0 (XACK on completion)", got)
	}
	msgs, err := rawRedis(t, addr).XRange(context.Background(), redisq.DoneStream, "-", "+").Result()
	if err != nil {
		t.Fatalf("cards:done: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("cards:done entries = %d, want 1", len(msgs))
	}
	got := msgs[0].Values
	for field, want := range map[string]string{
		"id": "id1", "label": "one", "bench": "bench-a", "exit": "0", "result": "RESULT: CARD-1 green",
	} {
		if have := fmt.Sprint(got[field]); have != want {
			t.Errorf("cards:done %s = %q, want %q", field, have, want)
		}
	}
	if job, _ := got["job"].(string); job == "" {
		t.Errorf("cards:done job = %v, want the job path", got["job"])
	}
}

// TestPullRenewsTheLease heartbeats the lease while a card runs: a lease that
// would have lapsed mid-card is still held after the renewal.
func TestPullRenewsTheLease(t *testing.T) {
	mr, c := testReadyGroup(t)
	addr := mr.Addr()
	seedReady(t, c, "id2", "two", "RESULT: CARD-2 green\n")
	slot := t.TempDir()

	old := pullRenewInterval
	pullRenewInterval = time.Hour
	t.Cleanup(func() { pullRenewInterval = old })

	started := make(chan struct{})
	doRenew := make(chan struct{})
	renewed := make(chan error, 1)
	finish := make(chan struct{})
	leaseKey := make(chan string, 1)
	useRunner(t, func(ctx context.Context, run cardRun, stdout, stderr io.Writer) (int, string, error) {
		leaseKey <- redisq.LeaseKey(run.EntryID)
		close(started)
		<-doRenew
		renewed <- run.renew()
		<-finish
		return 0, "RESULT: CARD-2 green", nil
	})

	done := make(chan int, 1)
	go func() {
		done <- run([]string{"pull", "--redis", addr, "--bench", "bench-a", "--slot-root", slot, "--once", "--lease", "1m"},
			strings.NewReader(""), io.Discard, io.Discard, time.Now().UTC())
	}()
	<-started
	key := <-leaseKey
	mr.FastForward(40 * time.Second) // a 1m lease now has 20s left
	close(doRenew)
	if err := <-renewed; err != nil {
		t.Fatalf("renew: %v", err)
	}
	mr.FastForward(50 * time.Second) // past the original life, inside the renewed one
	v, err := c.LeaseValue(context.Background(), key)
	if err != nil {
		t.Fatalf("lease value: %v", err)
	}
	if v != "bench-a" {
		t.Errorf("lease after renewal = %q, want bench-a (the renewal did not extend it)", v)
	}
	close(finish)
	if code := <-done; code != 0 {
		t.Errorf("pull exit %d, want 0", code)
	}
}

// TestPullReclaimsALapsedLease is SPEC-JOBS rule 2's remedy: a bench whose
// lease lapsed has its card reclaimed by the next bench through XAUTOCLAIM.
func TestPullReclaimsALapsedLease(t *testing.T) {
	mr, c := testReadyGroup(t)
	addr := mr.Addr()
	t0 := time.Now().UTC()
	mr.SetTime(t0)
	seedReady(t, c, "id3", "three", "RESULT: CARD-3 green\n")

	// bench-a claims the card, then dies: the lease lapses and time moves on.
	entry, err := c.ReadGroup(context.Background(), redisq.ReadyStream, redisq.Group, "bench-a", 0)
	if err != nil || entry == nil {
		t.Fatalf("bench-a claim: entry=%v err=%v", entry, err)
	}
	if _, err := c.AcquireLease(context.Background(), redisq.LeaseKey(entry.ID), "bench-a", time.Minute); err != nil {
		t.Fatalf("bench-a lease: %v", err)
	}
	mr.FastForward(2 * time.Minute)
	mr.SetTime(t0.Add(2 * time.Minute))

	got := ""
	gotBench := ""
	useRunner(t, func(ctx context.Context, run cardRun, stdout, stderr io.Writer) (int, string, error) {
		got = run.ID
		gotBench = run.Bench
		return 0, "RESULT: CARD-3 green", nil
	})

	var errb bytes.Buffer
	code := run([]string{"pull", "--redis", addr, "--bench", "bench-b", "--slot-root", t.TempDir(), "--once", "--lease", "1m"},
		strings.NewReader(""), io.Discard, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("pull exit %d: %s", code, errb.String())
	}
	if got != "id3" || gotBench != "bench-b" {
		t.Errorf("reclaimed run = %q by %q, want id3 by bench-b", got, gotBench)
	}
	if have := readyPending(t, addr); have != 0 {
		t.Errorf("pending after reclaim = %d, want 0", have)
	}
}
