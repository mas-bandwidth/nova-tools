package reconcile_test

import (
	"strings"
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

// TestMergeBriefIncludesReadRefusal verifies that MergeBrief formats
// the indented REFUSED READ lines under members that have a read refusal.
func TestMergeBriefIncludesReadRefusal(t *testing.T) {
	t.Parallel()

	card := reconcile.MergeCard{
		Stream: "landing",
		Slug:   "landing",
		Base:   "dev",
		Repo:   "mas-bandwidth/nova-tools",
		Members: []reconcile.MergeMember{
			{
				Task:        "build-1",
				PR:          "101",
				Order:       1,
				ReadRefusal: `REFUSED READ score=none head=aaaaaaaa remedy="nova-sprint read brief --pr 101"`,
			},
			{
				Task:        "build-2",
				PR:          "102",
				Order:       2,
				ReadRefusal: `REFUSED READ score=7 head=bbbbbbbb remedy="nova-sprint read brief --pr 102"`,
			},
			{
				Task:  "build-3",
				PR:    "103",
				Order: 3,
			},
		},
	}
	brief := reconcile.MergeBrief(card)
	if !strings.Contains(brief, `REFUSED READ score=none head=aaaaaaaa remedy="nova-sprint read brief --pr 101"`) {
		t.Fatalf("brief missing score=none refusal:\n%s", brief)
	}
	if !strings.Contains(brief, `REFUSED READ score=7 head=bbbbbbbb remedy="nova-sprint read brief --pr 102"`) {
		t.Fatalf("brief missing score=7 refusal:\n%s", brief)
	}
}
