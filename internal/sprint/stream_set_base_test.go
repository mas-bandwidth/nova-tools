package sprint

import (
	"testing"

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
	p := StreamSetBase(w.s, []string{"s1"}, "sprint/new-branch", "coordinator")
	w.must(p) // Apply the plan to the world

	// Check that s1-1's brief was updated
	s11 := w.s.Work.Placed("s1-1")
	newBrief1 := s11.F("brief")
	assert.Contains(t, newBrief1, "BASE: sprint/new-branch", "s1-1 BASE should be updated")

	// Check that s1-2's brief was updated (BASE added)
	s12 := w.s.Work.Placed("s1-2")
	newBrief2 := s12.F("brief")
	assert.Contains(t, newBrief2, "BASE: sprint/new-branch", "s1-2 BASE should be added")
}

// TestStreamSetBaseListsDealtCards tests that dealt cards keep their base and are listed.
func TestStreamSetBaseListsDealtCards(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")

	// Add a card and deal it
	brief := "REPO: mas-bandwidth/nova-tools\nBASE: old/dealt\nPATHS: cmd/test.go\n\nThe task.\n"
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Brief: brief, Who: "coordinator"}))

	// Deal the card (set attempt > 0)
	c := w.s.Work.Placed("s1-1")
	c.Fields["attempt"] = "1"

	// Run stream set --base
	p := StreamSetBase(w.s, []string{"s1"}, "sprint/new-branch", "coordinator")
	w.must(p) // Apply the plan to the world

	// Should list the dealt card
	assert.NotEmpty(t, p.Units, "should list dealt cards")
	assert.Contains(t, p.Units[0].Moved, "keeps its base", "should note it keeps its base")

	// Verify BASE wasn't changed
	newBrief := w.s.Work.Placed("s1-1").F("brief")
	assert.Contains(t, newBrief, "BASE: old/dealt", "dealt card should keep its base")
}

// TestStreamSetBaseRefusesNonExistentStream tests that non-existent streams are refused.
func TestStreamSetBaseRefusesNonExistentStream(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")

	// Run stream set --base on non-existent stream
	p := StreamSetBase(w.s, []string{"nonexistent"}, "sprint/new-branch", "coordinator")

	assert.NotEmpty(t, p.Refused, "should refuse non-existent stream")
	assert.Contains(t, p.Refused[0].Why, "no stream nonexistent")
}

// TestBriefSetBaseUpdatesExistingBASE tests that briefSetBase updates an existing BASE line.
func TestBriefSetBaseUpdatesExistingBASE(t *testing.T) {
	t.Parallel()
	brief := "REPO: test\nBASE: old/branch\nPATHS: cmd/test.go\n\nTask.\n"
	result := briefSetBase(brief, "new/branch")
	assert.Contains(t, result, "BASE: new/branch")
	assert.NotContains(t, result, "old/branch")
}

// TestBriefSetBaseAddsBASE tests that briefSetBase adds a BASE line when one doesn't exist.
func TestBriefSetBaseAddsBASE(t *testing.T) {
	t.Parallel()
	brief := "REPO: test\nPATHS: cmd/test.go\n\nTask.\n"
	result := briefSetBase(brief, "new/branch")
	assert.Contains(t, result, "BASE: new/branch")
	assert.Contains(t, result, "REPO: test")
}
