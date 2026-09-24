package routes

import (
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

func held(h *HoldSet) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.holds)
}

func TestSetTrimsKeyConsistently(t *testing.T) {
	var h HoldSet
	h.Set("  my-route  ", time.Minute, t0)
	for _, q := range []string{"my-route", "  my-route  ", "\tmy-route\n"} {
		if !h.IsHeld(q, t0) {
			t.Fatalf("IsHeld(%q) = false after Set(\"  my-route  \")", q)
		}
	}
	snap := h.Snapshot(t0)
	if len(snap) != 1 || snap[0].Route != "my-route" {
		t.Fatalf("Snapshot = %+v, want one hold keyed my-route", snap)
	}
	if !h.Lift("  my-route  ") {
		t.Fatal("Lift(\"  my-route  \") = false, want true")
	}
	if h.IsHeld("my-route", t0) {
		t.Fatal("route still held after Lift")
	}
}

func TestBlankAndNegative(t *testing.T) {
	var h HoldSet
	h.Set("   ", time.Minute, t0)
	h.Set("r", -time.Second, t0)
	if n := held(&h); n != 0 {
		t.Fatalf("holds = %d, want 0", n)
	}
	if h.IsHeld("  ", t0) || h.Lift("") {
		t.Fatal("blank route reported held/lifted")
	}
}

func TestZeroTTLLifts(t *testing.T) {
	var h HoldSet
	h.Set("r", time.Minute, t0)
	h.Set(" r ", 0, t0)
	if h.IsHeld("r", t0) {
		t.Fatal("Set(ttl=0) did not lift")
	}
}

func TestExpiryAndRefresh(t *testing.T) {
	var h HoldSet
	h.Set("r", time.Minute, t0)
	if !h.IsHeld("r", t0.Add(59*time.Second)) {
		t.Fatal("not held before expiry")
	}
	h.Set("r", time.Minute, t0.Add(30*time.Second)) // refresh, latest wins
	if !h.IsHeld("r", t0.Add(80*time.Second)) {
		t.Fatal("refresh did not extend expiry")
	}
	if h.IsHeld("r", t0.Add(90*time.Second)) {
		t.Fatal("held at expiry instant")
	}
	if n := held(&h); n != 0 {
		t.Fatalf("expired IsHeld left %d holds, want 0", n)
	}
}

func TestSnapshotPrunesExpired(t *testing.T) {
	var h HoldSet
	h.Set("b", time.Minute, t0)
	h.Set("a", time.Hour, t0)
	h.Set("c", time.Second, t0)
	snap := h.Snapshot(t0.Add(2 * time.Minute))
	if len(snap) != 1 || snap[0].Route != "a" {
		t.Fatalf("Snapshot = %+v, want only a", snap)
	}
	if n := held(&h); n != 1 {
		t.Fatalf("Snapshot left %d holds in the map, want 1 (expired must be pruned)", n)
	}
	h.Set("z", time.Hour, t0)
	h.Set("m", time.Hour, t0)
	snap = h.Snapshot(t0)
	if len(snap) != 3 || snap[0].Route != "a" || snap[1].Route != "m" || snap[2].Route != "z" {
		t.Fatalf("Snapshot not sorted: %+v", snap)
	}
	if got := h.Snapshot(t0.Add(48 * time.Hour)); got != nil {
		t.Fatalf("Snapshot of all-expired = %+v, want nil", got)
	}
	if n := held(&h); n != 0 {
		t.Fatalf("holds = %d after all expired, want 0", n)
	}
}

func TestClock(t *testing.T) {
	var zero HoldSet
	if zero.Clock() == nil {
		t.Fatal("zero Clock is nil")
	}
	h := NewHoldSet(func() time.Time { return t0 })
	if !h.Clock()().Equal(t0) {
		t.Fatal("injected clock not returned")
	}
}

func TestConcurrent(t *testing.T) {
	var h HoldSet
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				h.Set(" r ", time.Minute, t0)
				h.IsHeld("r", t0)
				h.Snapshot(t0)
				h.Lift("r")
			}
		}()
	}
	wg.Wait()
}
