package reconcile

import (
	"testing"
	"time"
)

// TestDutyMustStop: the lease-bound check each duty runs before starting new
// work (#3805). Fenced stops immediately; under-margin stops with the margin
// left.
func TestDutyMustStop(t *testing.T) {
	now := time.Now()
	ttl := 6 * time.Second
	margin := DefaultWriteMargin

	// Fenced lease stops.
	fl := &Lease{renewed: now, ttl: ttl, fence: ErrFenced}
	if !DutyMustStop(fl, margin) {
		t.Fatal("fenced lease must stop")
	}

	// Under margin stops.
	fl.fence = nil
	fl.renewed = now.Add(-ttl + 500*time.Millisecond) // 500ms left, less than 1s margin
	if !DutyMustStop(fl, margin) {
		t.Fatal("under-margin lease must stop")
	}

	// Plenty of time: does not stop.
	fl.renewed = now
	if DutyMustStop(fl, margin) {
		t.Fatal("plenty of time must not stop")
	}

	// Zero margin uses default.
	fl.renewed = now.Add(-ttl + 500*time.Millisecond)
	if !DutyMustStop(fl, 0) {
		t.Fatal("zero margin must default to DefaultWriteMargin and stop")
	}
}

// TestDutyStop returns the detailed fence/margin info.
func TestDutyStop(t *testing.T) {
	now := time.Now()
	ttl := 6 * time.Second

	// Fenced: returns fenced=true.
	fl := &Lease{renewed: now, ttl: ttl, fence: ErrFenced}
	fenced, left := DutyStop(fl, DefaultWriteMargin)
	if !fenced || left != 0 {
		t.Fatalf("fenced: fenced=%v left=%v; want true 0", fenced, left)
	}

	// Under margin: returns fenced=false with remaining.
	fl.fence = nil
	fl.renewed = now.Add(-ttl + 500*time.Millisecond)
	fenced, left = DutyStop(fl, DefaultWriteMargin)
	if fenced || left <= 0 || left > DefaultWriteMargin {
		t.Fatalf("under margin: fenced=%v left=%v; want false, (0, margin]", fenced, left)
	}

	// Plenty of time: returns fenced=false with full remaining.
	fl.renewed = now
	fenced, left = DutyStop(fl, DefaultWriteMargin)
	if fenced || left <= DefaultWriteMargin {
		t.Fatalf("plenty: fenced=%v left=%v; want false, >margin", fenced, left)
	}
}

// TestSlowDutyEndsInsideWindow is the #3805 DONE-WHEN test: a duty whose
// work items take time is bounded by the lease and stops starting new work
// when less than the write margin is left, leaving the pass ending inside
// the window.
func TestSlowDutyEndsInsideWindow(t *testing.T) {
	// A lease with 2 s left: enough for one item but not two with a 1s margin.
	now := time.Now()
	ttl := 6 * time.Second
	l := &Lease{renewed: now.Add(-4 * time.Second), ttl: ttl, fence: nil}

	margin := DefaultWriteMargin // 1s
	// First check: 2s left, more than margin: OK to start.
	if DutyMustStop(l, margin) {
		t.Fatal("2s left must not stop with 1s margin")
	}

	// Simulate one work item taking 1.5s.
	l.renewed = now.Add(-5500 * time.Millisecond) // 500ms left

	// Second check: 500ms left, less than margin: must stop.
	if !DutyMustStop(l, margin) {
		t.Fatal("500ms left must stop with 1s margin")
	}

	// The pass ends at 500ms, which is inside the write window [0, 1s].
	left := l.Remaining()
	if left <= 0 || left > margin {
		t.Fatalf("pass ended with %s left, want inside (0, %s]", left, margin)
	}
}
