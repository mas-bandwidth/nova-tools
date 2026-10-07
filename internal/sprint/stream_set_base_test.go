package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStreamSetBaseRepointsQueuedCards tests that stream set --base re-points
// ready and queued cards to a live base, and lists dealt ones that keep their base.
func TestStreamSetBaseRepointsQueuedCards(t *testing.T) {
	t.Parallel()
	// Set up a stream with ready and waiting cards
	w := newWorld(t, "reader-a")
	// Add a card with REPO and BASE (this one will be ready)
	brief := "REPO: mas-bandwidth/nova-tools\nBASE: old/branch\nPATHS: cmd/test.go\n\nThe task.\n"
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Brief: brief, Who: "coordinator"}))
	// Add a card without BASE that needs s1-1 (this one will be waiting)
	briefNoBase := "REPO: mas-bandwidth/nova-tools\nPATHS: cmd/test2.go\n\nAnother task.\n"
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-2"}, Brief: briefNoBase, Needs: []string{"s1-1"}, Who: "coordinator"}))

	// s1-1 is ready, s1-2 is waiting (needs s1-1)
	require.Equal(t, Ready, w.s.Work.Placed("s1-1").Col, "s1-1 should be ready")
	require.Equal(t, Waiting, w.s.Work.Placed("s1-2").Col, "s1-2 should be waiting")

	// Run stream set --base
	p := Set(w.s, SetReq{Streams: []string{"s1"}, Base: "sprint/new-branch", Who: "coordinator"})
	w.must(p) // Apply the plan to the world

	// Check that s1-1's brief was updated
	s11 := w.s.Work.Placed("s1-1")
	newBrief1 := s11.F("brief")
	baseVal1, ok := cardhdr.Value(newBrief1, "BASE")
	assert.True(t, ok, "s1-1 should have BASE line")
	ref1, _, _ := cardhdr.ParseBase(baseVal1)
	assert.Equal(t, "sprint/new-branch", ref1, "s1-1 BASE should be updated")

	// Check that s1-2's brief was updated (BASE added)
	s12 := w.s.Work.Placed("s1-2")
	newBrief2 := s12.F("brief")
	baseVal2, ok := cardhdr.Value(newBrief2, "BASE")
	assert.True(t, ok, "s1-2 should have BASE line")
	ref2, _, _ := cardhdr.ParseBase(baseVal2)
	assert.Equal(t, "sprint/new-branch", ref2, "s1-2 BASE should be added")
}

// TestStreamSetBaseListsDealtCards tests that dealt cards keep their base and are listed.
func TestStreamSetBaseListsDealtCards(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")

	// Add a card and deal it
	brief := "REPO: mas-bandwidth/nova-tools\nBASE: old/dealt\nPATHS: cmd/test.go\n\nThe task.\n"
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Brief: brief, Who: "coordinator"}))

	// Deal the card (set attempt > 0 and col = ready or working)
	c := w.s.Work.Placed("s1-1")
	c.Fields["attempt"] = "1"
	c.Col = Ready

	// Run stream set --base
	p := Set(w.s, SetReq{Streams: []string{"s1"}, Base: "sprint/new-branch", Who: "coordinator"})
	w.must(p) // Apply the plan to the world

	// Should list the dealt card
	assert.NotEmpty(t, p.Units, "should list dealt cards")
	assert.Contains(t, p.Units[0].Moved, "keeps its base", "should note it keeps its base")

	// Verify BASE wasn't changed
	newBrief := w.s.Work.Placed("s1-1").F("brief")
	baseVal, ok := cardhdr.Value(newBrief, "BASE")
	assert.True(t, ok, "BASE should exist")
	ref, _, _ := cardhdr.ParseBase(baseVal)
	assert.Equal(t, "old/dealt", ref, "dealt card should keep its base")
}

// TestStreamSetBaseRefusesNonExistentStream tests that non-existent streams are refused.
func TestStreamSetBaseRefusesNonExistentStream(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")

	// Run stream set --base on non-existent stream
	p := Set(w.s, SetReq{Streams: []string{"nonexistent"}, Base: "sprint/new-branch", Who: "coordinator"})

	assert.NotEmpty(t, p.Refused, "should refuse non-existent stream")
	assert.Contains(t, p.Refused[0].Why, "no stream nonexistent")
}
