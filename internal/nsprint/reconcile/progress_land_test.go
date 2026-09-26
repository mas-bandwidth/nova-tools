package reconcile_test

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
)

// TestStepCountsALandingPastItsWallAsStalled (nova-tools #4324 with #4319):
// land:slow:<stream> stalled=1 makes the stream stalled whatever its
// counts say, with blocked_since as the episode key so the ask fires once;
// when the landing clears the stream is idle again.
func TestStepCountsALandingPastItsWallAsStalled(t *testing.T) {
	t.Parallel()
	t0 := time.UnixMilli(1700000000000)
	window := 30 * time.Minute
	s := reconcile.Sample{Stream: "cards", Merging: 3, LandStalled: true}
	st := reconcile.Step(reconcile.State{}, s, false, t0, window)
	if st.Status != reconcile.StatusStalled || st.BlockedSince != t0.UnixMilli() || !st.NeedsAsk() {
		t.Fatalf("first run: %+v", st)
	}
	st.AskedAt = st.BlockedSince
	st = reconcile.Step(st, s, false, t0.Add(time.Minute), window)
	if st.Status != reconcile.StatusStalled || st.BlockedSince != t0.UnixMilli() || st.NeedsAsk() {
		t.Fatalf("same episode asks once: %+v", st)
	}
	s.LandStalled = false
	st = reconcile.Step(st, s, false, t0.Add(2*time.Minute), window)
	if st.Status != reconcile.StatusIdle || st.BlockedSince != 0 {
		t.Fatalf("cleared: %+v", st)
	}
}
