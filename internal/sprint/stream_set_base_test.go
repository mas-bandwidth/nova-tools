package sprint

import (
	"testing"
)

// TestStreamSetBaseRepointsQueuedCards tests stream set --base re-points a stream to a live base.
func TestStreamSetBaseRepointsQueuedCards(t *testing.T) {
	t.Parallel()

	w := newWorld(t, "reader-a")

	// Add cards to stream s1 (all waiting)
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{
		{ID: "s1-1", Brief: "REPO: mas-bandwidth/nova-tools\nBASE: old/branch\nTASK: waiting"},
		{ID: "s1-2", Brief: "REPO: mas-bandwidth/nova-tools\nBASE: old/branch\nTASK: waiting2"},
	}}))

	// Test: stream set --base should update waiting cards in s1
	p := Set(w.s, SetReq{Streams: []string{"s1"}, Base: "new/branch"})
	w.must(p)

	// Check s1-1 got updated base
	s1_1 := w.s.Work.Placed("s1-1")
	if s1_1 == nil {
		t.Fatal("s1-1 not found")
	}
	if s1_1.F("base") != "new/branch" {
		t.Errorf("s1-1 base = %q, want %q", s1_1.F("base"), "new/branch")
	}

	// Check s1-2 also got updated base
	s1_2 := w.s.Work.Placed("s1-2")
	if s1_2 == nil {
		t.Fatal("s1-2 not found")
	}
	if s1_2.F("base") != "new/branch" {
		t.Errorf("s1-2 base = %q, want %q", s1_2.F("base"), "new/branch")
	}
}

// TestStreamSetBaseNoBaseUpdates tests that --base with no waiting/ready/queued cards still succeeds
func TestStreamSetBaseNoBaseUpdates(t *testing.T) {
	t.Parallel()

	w := newWorld(t, "reader-a")

	// Add a card and deal it
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{
		{ID: "s1-1", Brief: "REPO: mas-bandwidth/nova-tools\nBASE: old/branch\nTASK: working"},
	}}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(Deal(w.s, DealReq{Sel: Sel{Limit: 1}}))
	w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{Limit: 1}}))
	// Card is now working, Set should not change it

	// stream set --base should still succeed but not update any cards
	p := Set(w.s, SetReq{Streams: []string{"s1"}, Base: "new/branch"})
	w.must(p)

	// Check s1-1 kept its base
	s1_1 := w.s.Work.Placed("s1-1")
	if s1_1 == nil {
		t.Fatal("s1-1 not found")
	}
	if s1_1.F("base") != "old/branch" {
		t.Errorf("s1-1 base = %q, want %q (should be unchanged)", s1_1.F("base"), "old/branch")
	}
}

// TestStreamSetBaseEmptyStreams tests that --base without --stream is refused
func TestStreamSetBaseEmptyStreams(t *testing.T) {
	t.Parallel()

	w := newWorld(t, "reader-a")

	// Set with --base without --stream should be refused
	p := Set(w.s, SetReq{Base: "new/branch"})
	if len(p.Refused) == 0 {
		t.Fatal("Set without streams should be refused")
	}
	found := false
	for _, r := range p.Refused {
		if r.Why == "--base is a stream's, not the sprint's: nova-sprint stream set <stream> --base <branch>" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("want '--base is a stream's' in refusal, got %v", p.Refused)
	}
}
