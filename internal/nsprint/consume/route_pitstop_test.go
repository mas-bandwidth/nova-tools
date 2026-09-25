package consume

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
)

// rtBuf is a writer the router's goroutines and the test share.
type rtBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *rtBuf) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *rtBuf) count(prefix string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, l := range strings.Split(w.b.String(), "\n") {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

func (w *rtBuf) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// TestRoutePitstopDispatchesNothing: while s:<S>:pitstop holds the whole
// sprint the router keeps its lease but passes no rule, and prints
// `PITSTOP idle` once, not every tick; `pitstop clear` resumes the rules on
// the next tick with one `PITSTOP resume` line.
func TestRoutePitstopDispatchesNothing(t *testing.T) {
	t.Parallel()
	st, client := controlRedis(t)
	ctx := context.Background()
	const S = "ctl-route-pitstop"
	must(t, client.HSet(ctx, "s:"+S, "status", "open").Err())
	if _, err := pitstop.Set(ctx, client, S, "glenn", "rest tonight", false, ""); err != nil {
		t.Fatal(err)
	}
	a, b := &rtFake{}, &rtFake{}
	out := &rtBuf{}
	r := &Router{
		Store: st, Sprint: S, Instance: "ctl-inst", Host: "ctl-host",
		Rules: []RouteRule{{Name: "a", Handler: a}, {Name: "b", Handler: b}},
		TTL:   6 * time.Second, Renew: 20 * time.Millisecond, Backoff: 10 * time.Millisecond, Out: out,
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- r.Run(runCtx) }()
	t.Cleanup(func() {
		stop()
		<-done
	})

	// The lease is taken (the router is up) and many ticks pass held.
	rtUntil(t, "the route lease", func() bool { return client.Exists(ctx, LeaseKey(S)).Val() == 1 })
	// The held tick's own receipt is the event to wait for, never a fixed
	// sleep (internal/ci TestNoFixedWaitsOnTheCIPath).
	rtUntil(t, "the PITSTOP idle line", func() bool { return out.count("PITSTOP idle") >= 1 })
	if n := a.passes.Load() + b.passes.Load(); n != 0 {
		t.Fatalf("held: rules passed %d times, want 0", n)
	}
	if n := out.count("PITSTOP idle"); n != 1 {
		t.Fatalf("held: %d PITSTOP idle lines, want exactly 1:\n%s", n, out.String())
	}
	if !strings.Contains(out.String(), "PITSTOP idle sprint="+S+" scope=all") || !strings.Contains(out.String(), `why="rest tonight"`) {
		t.Fatalf("held: the idle line does not name sprint, scope and why:\n%s", out.String())
	}

	if _, err := pitstop.Clear(ctx, client, S, "glenn", ""); err != nil {
		t.Fatal(err)
	}
	rtUntil(t, "both rules pass after clear", func() bool { return a.passes.Load() > 0 && b.passes.Load() > 0 })
	if n := out.count("PITSTOP resume"); n != 1 {
		t.Fatalf("cleared: %d PITSTOP resume lines, want exactly 1:\n%s", n, out.String())
	}
	if n := out.count("PITSTOP idle"); n != 1 {
		t.Fatalf("cleared: %d PITSTOP idle lines, want still 1:\n%s", n, out.String())
	}
}
