package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// runBeatLoop starts benchBeatLoop on a tick channel the test drives and
// returns the channel, a stop that waits for the exit code, and the stderr.
func runBeatLoop(t *testing.T, beat func(context.Context) (life.BenchResult, error)) (chan time.Time, func() int, *bytes.Buffer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time)
	var errOut bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- benchBeatLoop(ctx, ticks, time.Second, "b1", &errOut, beat) }()
	stop := func() int { cancel(); return <-done }
	t.Cleanup(func() { cancel() })
	return ticks, stop, &errOut
}

// TestBenchBeatTwoTicksOneClient (#3372): the loop's ticks ride the one
// connection the verb opened; two ticks dial nothing new.
func TestBenchBeatTwoTicksOneClient(t *testing.T) {
	mr := miniredis.RunT(t)
	ctx := context.Background()
	st, err := store.OpenSingle(ctx, mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	calls := 0
	beaten := make(chan error, 4)
	// miniredis has no FCALL: the beat is one HSET through the same client,
	// which is what the loop is under test for.
	beat := func(ctx context.Context) (life.BenchResult, error) {
		calls++
		err := st.Client().HSet(ctx, "bench:b1:beat", "at", calls).Err()
		beaten <- err
		return life.BenchResult{Accepted: true, Owner: "s1"}, err
	}
	ticks, stop, errOut := runBeatLoop(t, beat)
	t0 := time.Unix(1790186400, 0)
	for i := 0; i < 2; i++ {
		ticks <- t0.Add(time.Duration(i) * time.Second)
		if err := <-beaten; err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	if code := stop(); code != 0 {
		t.Fatalf("loop exit %d, want 0; stderr %q", code, errOut)
	}
	if calls != 2 {
		t.Fatalf("beats %d, want 2", calls)
	}
	if n := mr.TotalConnectionCount(); n != 1 {
		t.Fatalf("connections dialed %d, want 1 (open + two ticks on one client)", n)
	}
	if at := mr.HGet("bench:b1:beat", "at"); at != "2" {
		t.Fatalf("beat at %q, want 2", at)
	}
}

// TestBenchBeatReconnectsWithBackoff (#3372): a failed beat waits one
// interval before the next attempt (a tick inside the wait is skipped), and
// that attempt redials through the same client once Redis is back.
func TestBenchBeatReconnectsWithBackoff(t *testing.T) {
	mr := miniredis.RunT(t)
	ctx := context.Background()
	st, err := store.OpenSingle(ctx, mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	calls, fails := 0, 0
	beaten := make(chan error, 4)
	beat := func(ctx context.Context) (life.BenchResult, error) {
		calls++
		err := st.Client().HSet(ctx, "bench:b1:beat", "at", calls).Err()
		if err != nil {
			fails++
		}
		beaten <- err
		return life.BenchResult{Accepted: true, Owner: "s1"}, err
	}
	ticks, stop, errOut := runBeatLoop(t, beat)
	t0 := time.Unix(1790186400, 0)
	ticks <- t0
	if err := <-beaten; err != nil {
		t.Fatalf("first beat: %v", err)
	}
	addr := mr.Addr()
	mr.Close()
	ticks <- t0.Add(1 * time.Second) // fails: backoff 1 s
	if err := <-beaten; err == nil {
		t.Fatal("a beat with Redis down succeeded")
	}
	if err := mr.StartAddr(addr); err != nil { // the same address, as a restarted Redis
		t.Fatal(err)
	}
	// The restarted server counts connections from zero. With its one
	// connection gone the client probes the address once a second on its own
	// and clears its cached dial error before closing the probe; wait (wall
	// clock) for the probe to open and close so the retry below dials.
	probed := func() bool { return mr.TotalConnectionCount() > 0 && mr.CurrentConnectionCount() == 0 }
	for deadline := time.Now().Add(5 * time.Second); !probed(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the client never probed the restarted Redis")
		}
	}
	ticks <- t0.Add(1500 * time.Millisecond) // inside the backoff: skipped
	ticks <- t0.Add(2 * time.Second)         // retried: redials
	if err := <-beaten; err != nil {
		t.Fatalf("retry after restart: %v", err)
	}
	if code := stop(); code != 0 {
		t.Fatalf("loop exit %d, want 0; stderr %q", code, errOut)
	}
	if calls != 3 || fails != 1 {
		t.Fatalf("beats %d fails %d, want 3 and 1 (the tick inside the backoff is skipped); stderr %q", calls, fails, errOut.String())
	}
	if !strings.Contains(errOut.String(), "retry in 1s") {
		t.Fatalf("stderr lacks the backoff: %q", errOut)
	}
	if at := mr.HGet("bench:b1:beat", "at"); at != "3" {
		t.Fatalf("beat after reconnect at %q, want 3", at)
	}
}

func TestBenchBeatBackoffDoublesToCap(t *testing.T) {
	var got []time.Duration
	var b time.Duration
	for i := 0; i < 7; i++ {
		b = benchBeatBackoff(b, time.Second)
		got = append(got, b)
	}
	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 16 * time.Second, 16 * time.Second}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("backoff %v, want %v", got, want)
		}
	}
}

// TestBenchBeatLostOwnershipExits2: a BUSY beat ends the loop with 2.
func TestBenchBeatLostOwnershipExits2(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time, 1)
	ticks <- time.Unix(1790186400, 0)
	var errOut bytes.Buffer
	code := benchBeatLoop(ctx, ticks, time.Second, "b1", &errOut, func(context.Context) (life.BenchResult, error) {
		return life.BenchResult{Accepted: false, Owner: "other"}, nil
	})
	if code != 2 || !strings.Contains(errOut.String(), "lost ownership to other") {
		t.Fatalf("exit %d stderr %q, want 2 and the owner", code, errOut.String())
	}
}
