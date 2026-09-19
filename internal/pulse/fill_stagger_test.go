package pulse

// The stagger and the max-inflight cap (nova-tools#1785). Twelve simultaneous launches
// tripped sshd MaxStartups; 119 cards started within one minute against a single provider
// key. Nothing today paces two launches to the SAME bench: the fill's round-robin calls
// its launcher for every ready card a bench has capacity for with no gap, and the
// per-route cap in batch is a different axis -- two benches can share one key while each
// opens its own ssh session.
//
// These tests prove the fill's half on a MONOTONIC counter, never on wall-clock duration:
// a fake Now/Sleep pair over one *time.Time, where Sleep ADVANCES the fake clock by its
// argument instead of really sleeping.

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// benchConn is one open connection the fake launcher holds to a bench.
type benchConn struct {
	bench  string
	openAt time.Time
}

// staggerLauncher treats a connection as OPEN at now() and expires any prior connection
// whose openAt + holdFor <= now(); it tracks the running open count and its peak, the way
// inflight.go's own note/peak does.
type staggerLauncher struct {
	now      func() time.Time
	holdFor  time.Duration
	conns    []benchConn
	peak     int
	launched []string
}

func (l *staggerLauncher) Launch(bench, card string) error {
	now := l.now()
	kept := l.conns[:0]
	for _, c := range l.conns {
		if now.Sub(c.openAt) < l.holdFor {
			kept = append(kept, c)
		}
	}
	kept = append(kept, benchConn{bench, now})
	l.conns = kept
	if len(kept) > l.peak {
		l.peak = len(kept)
	}
	l.launched = append(l.launched, bench+" "+card)
	return nil
}

// TestFillNeverExceedsStaggerImpliedConcurrency: five ready cards, one bench, capacity
// five, holdFor 2s, --stagger 3s. A launch wave must never exceed the stagger's implied
// concurrency (ceil(holdFor/stagger) = 1): the second launch to the same bench waits the
// remaining gap, and by then the first connection has expired.
func TestFillNeverExceedsStaggerImpliedConcurrency(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	for i := 1; i <= 5; i++ {
		writeCard(t, ready, fmt.Sprintf("card-%03d.md", i), "a card\n")
	}
	const holdFor = 2 * time.Second
	const gap = 3 * time.Second
	clk := time.Unix(1000, 0)
	now := func() time.Time { return clk }
	sleep := func(d time.Duration) { clk = clk.Add(d) }
	l := &staggerLauncher{now: now, holdFor: holdFor}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:  []string{"bench-a"},
		Once:     true,
		Now:      now,
		Sleep:    sleep,
		Stagger:  gap,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: laneCap{"bench-a": 5},
		Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	implied := int((holdFor + gap - 1) / gap)
	if l.peak > implied {
		t.Fatalf("peak open connections to one bench = %d, want at most the stagger-implied %d", l.peak, implied)
	}
}

// TestFillNeverExceedsMaxInflightPerRoute: five ready cards, one bench, capacity five,
// --max-inflight 2 and --stagger 0 (to isolate the mechanism). Exactly two launch; the
// other three are held behind the route's live markers and stay --ready.
func TestFillNeverExceedsMaxInflightPerRoute(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	for i := 1; i <= 5; i++ {
		writeCard(t, ready, fmt.Sprintf("card-%03d.md", i), "a card\n")
	}
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines:    machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:     []string{"bench-a"},
		Once:        true,
		MaxInflight: 2,
		Stdout:      &out,
		Stderr:      &errb,
		Capacity:    laneCap{"bench-a": 5},
		Launcher:    l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 2 {
		t.Fatalf("launched = %d, want exactly 2 under --max-inflight 2: %q", len(l.calls), l.calls)
	}
	if got := len(readyCards(ready)); got != 3 {
		t.Fatalf("ready holds %d cards, want the 3 held ones", got)
	}
	if got := len(readyCards(launched)); got != 2 {
		t.Fatalf("launched holds %d cards, want the route's 2 live markers", got)
	}
	if held := strings.Count(out.String(), "FILL HELD"); held != 3 {
		t.Fatalf("FILL HELD lines = %d, want 3:\n%s", held, out.String())
	}
}

// TestFillWithZeroStaggerStillLaunchesEveryCard: --stagger 0 is off, and the fix must
// never degrade to "stagger forever": every ready card launches in the one tick.
func TestFillWithZeroStaggerStillLaunchesEveryCard(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	for i := 1; i <= 5; i++ {
		writeCard(t, ready, fmt.Sprintf("card-%03d.md", i), "a card\n")
	}
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:  []string{"bench-a"},
		Once:     true,
		Stagger:  0,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: laneCap{"bench-a": 5},
		Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 5 {
		t.Fatalf("launched = %d, want all 5 under --stagger 0: %q", len(l.calls), l.calls)
	}
}

// TestFillWithMaxInflightZeroIsUncapped: --max-inflight 0 is off, today's behaviour: every
// ready card launches in the one tick, matching an uncapped fill byte for byte.
func TestFillWithMaxInflightZeroIsUncapped(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	for i := 1; i <= 5; i++ {
		writeCard(t, ready, fmt.Sprintf("card-%03d.md", i), "a card\n")
	}
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines:    machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:     []string{"bench-a"},
		Once:        true,
		MaxInflight: 0,
		Stdout:      &out,
		Stderr:      &errb,
		Capacity:    laneCap{"bench-a": 5},
		Launcher:    l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 5 {
		t.Fatalf("launched = %d, want all 5 under --max-inflight 0: %q", len(l.calls), l.calls)
	}
}
