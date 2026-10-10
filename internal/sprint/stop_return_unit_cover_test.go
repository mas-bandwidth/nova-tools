package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// StopReturnCover test the StopReturn planner in internal/sprint/stop_return.go.
// All tests are table-driven, open with t.Parallel(), and use hand-made tables.
// No store, clock, sleep, subprocess, network or server is used.

// testSnap returns a fresh snapshot with Now set to a fixed time.
func testSnap() *Snapshot {
	return &Snapshot{Now: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
}

// TestSprintStopReturnCoverBlankReason tests a blank reason refusal.
func TestSprintStopReturnCoverBlankReason(t *testing.T) {
	t.Parallel()
	s := testSnap()
	p := StopReturn(s, StopReturnReq{As: "row1", Reason: ""})
	assert.Len(t, p.Refused, 1)
	assert.Equal(t, "stop-return", p.Refused[0].Key)
	assert.Equal(t, "name the observed cancellation acknowledgement in --reason", p.Refused[0].Why)
	assert.Len(t, p.Units, 0)
}

// TestSprintStopReturnCoverIDNamedTwice tests that a duplicate ID is refused.
func TestSprintStopReturnCoverIDNamedTwice(t *testing.T) {
	t.Parallel()
	s := testSnap()
	c1 := &Card{ID: "c1", Row: "row1", Col: Working, Rev: 1, Fields: map[string]string{
		"gen": "1", "stream": "s1",
	}}
	s.Fleet = NewTable(Fleet)
	s.Fleet.SetRows([]string{"row1"})
	s.Fleet.Put(c1)

	p := StopReturn(s, StopReturnReq{As: "row1", Reason: "cancel", IDs: []string{"c1", "c1"}, Gens: map[string]int{"c1": 1}})
	assert.Len(t, p.Refused, 1)
	assert.Equal(t, "c1", p.Refused[0].Key)
	assert.Equal(t, "named twice", p.Refused[0].Why)
	assert.Len(t, p.Units, 1)
	assert.Equal(t, "c1", p.Units[0].Key)
}

// TestSprintStopReturnCoverIDOnNoTable tests a card that does not exist on any table.
func TestSprintStopReturnCoverIDOnNoTable(t *testing.T) {
	t.Parallel()
	s := testSnap()
	s.Fleet = NewTable(Fleet)
	s.Fleet.SetRows([]string{"row1"})
	s.Readers = NewTable(Readers)

	p := StopReturn(s, StopReturnReq{As: "row1", Reason: "cancel", IDs: []string{"c1"}, Gens: map[string]int{"c1": 1}})
	assert.Len(t, p.Refused, 1)
	assert.Equal(t, "c1", p.Refused[0].Key)
	assert.Equal(t, "not a live card on row1", p.Refused[0].Why)
}

// TestSprintStopReturnCoverIDOnWrongRow tests a card on a different row.
func TestSprintStopReturnCoverIDOnWrongRow(t *testing.T) {
	t.Parallel()
	s := testSnap()
	c1 := &Card{ID: "c1", Row: "row2", Col: Working, Rev: 1, Fields: map[string]string{
		"gen": "1", "stream": "s1",
	}}
	s.Fleet = NewTable(Fleet)
	s.Fleet.SetRows([]string{"row1", "row2"})
	s.Fleet.Put(c1)

	p := StopReturn(s, StopReturnReq{As: "row1", Reason: "cancel", IDs: []string{"c1"}, Gens: map[string]int{"c1": 1}})
	assert.Len(t, p.Refused, 1)
	assert.Equal(t, "c1", p.Refused[0].Key)
	assert.Equal(t, "not a live card on row1", p.Refused[0].Why)
}

// TestSprintStopReturnCoverGenMissing tests a card with no gen field (counts as 1).
func TestSprintStopReturnCoverGenMissing(t *testing.T) {
	t.Parallel()
	s := testSnap()
	c1 := &Card{ID: "c1", Row: "row1", Col: Working, Rev: 1, Fields: map[string]string{
		"stream": "s1",
	}}
	s.Fleet = NewTable(Fleet)
	s.Fleet.SetRows([]string{"row1"})
	s.Fleet.Put(c1)

	p := StopReturn(s, StopReturnReq{As: "row1", Reason: "cancel", IDs: []string{"c1"}, Gens: map[string]int{"c1": 1}})
	assert.Len(t, p.Units, 1)
	assert.Equal(t, "c1", p.Units[0].Key)
	assert.Equal(t, "s1", p.Units[0].Stream)
	assert.Len(t, p.Units[0].Changes, 1)
	entry := p.Units[0].Changes[0].Entry
	assert.Equal(t, Ready, entry.Move.Col)
	assert.Equal(t, "2", entry.Set["gen"])
	assert.Equal(t, "1", entry.Set["stopped_from_gen"])
}

// TestSprintStopReturnCoverGenBelow1 tests a gen below 1.
func TestSprintStopReturnCoverGenBelow1(t *testing.T) {
	t.Parallel()
	s := testSnap()
	c1 := &Card{ID: "c1", Row: "row1", Col: Working, Rev: 1, Fields: map[string]string{
		"gen": "1", "stream": "s1",
	}}
	s.Fleet = NewTable(Fleet)
	s.Fleet.SetRows([]string{"row1"})
	s.Fleet.Put(c1)

	p := StopReturn(s, StopReturnReq{As: "row1", Reason: "cancel", IDs: []string{"c1"}, Gens: map[string]int{"c1": 0}})
	assert.Len(t, p.Refused, 1)
	assert.Equal(t, "c1", p.Refused[0].Key)
	assert.Equal(t, "name the generation cancelled as <card>@<gen>", p.Refused[0].Why)
}

// TestSprintStopReturnCoverStaleGen tests a stale generation.
func TestSprintStopReturnCoverStaleGen(t *testing.T) {
	t.Parallel()
	s := testSnap()
	c1 := &Card{ID: "c1", Row: "row1", Col: Working, Rev: 1, Fields: map[string]string{
		"gen": "2", "stream": "s1",
	}}
	s.Fleet = NewTable(Fleet)
	s.Fleet.SetRows([]string{"row1"})
	s.Fleet.Put(c1)

	p := StopReturn(s, StopReturnReq{As: "row1", Reason: "cancel", IDs: []string{"c1"}, Gens: map[string]int{"c1": 1}})
	assert.Len(t, p.Refused, 1)
	assert.Equal(t, "c1", p.Refused[0].Key)
	assert.Equal(t, "stale: generation 1 is not the live one (2)", p.Refused[0].Why)
}

// TestSprintStopReturnCoverWrongColumn tests a card in ready, not working.
func TestSprintStopReturnCoverWrongColumn(t *testing.T) {
	t.Parallel()
	s := testSnap()
	c1 := &Card{ID: "c1", Row: "row1", Col: Ready, Rev: 1, Fields: map[string]string{
		"gen": "1", "stream": "s1",
	}}
	s.Fleet = NewTable(Fleet)
	s.Fleet.SetRows([]string{"row1"})
	s.Fleet.Put(c1)

	p := StopReturn(s, StopReturnReq{As: "row1", Reason: "cancel", IDs: []string{"c1"}, Gens: map[string]int{"c1": 1}})
	assert.Len(t, p.Refused, 1)
	assert.Equal(t, "c1", p.Refused[0].Key)
	assert.Equal(t, "not in working (it is ready)", p.Refused[0].Why)
}

// TestSprintStopReturnCoverReaderCard tests a reader card in reading moves to asked.
func TestSprintStopReturnCoverReaderCard(t *testing.T) {
	t.Parallel()
	s := testSnap()
	c1 := &Card{ID: "c1", Row: "row1", Col: Reading, Rev: 1, Fields: map[string]string{
		"gen": "1", "stream": "s1",
	}}
	s.Readers = NewTable(Readers)
	s.Readers.SetRows([]string{"row1"})
	s.Readers.Put(c1)

	p := StopReturn(s, StopReturnReq{As: "row1", Reason: "cancel", IDs: []string{"c1"}, Gens: map[string]int{"c1": 1}})
	assert.Len(t, p.Units, 1)
	assert.Equal(t, "c1", p.Units[0].Key)
	assert.Equal(t, "s1", p.Units[0].Stream)
	assert.Len(t, p.Units[0].Changes, 1)
	entry := p.Units[0].Changes[0].Entry
	assert.Equal(t, Asked, entry.Move.Col)
}

// TestSprintStopReturnCoverWithProgress tests a card with FieldProgress is handled.
func TestSprintStopReturnCoverWithProgress(t *testing.T) {
	t.Parallel()
	s := testSnap()
	c1 := &Card{ID: "c1", Row: "row1", Col: Working, Rev: 1, Fields: map[string]string{
		"gen": "1", "stream": "s1", FieldProgress: "progress-value",
	}}
	s.Fleet = NewTable(Fleet)
	s.Fleet.SetRows([]string{"row1"})
	s.Fleet.Put(c1)

	p := StopReturn(s, StopReturnReq{As: "row1", Reason: "cancel", IDs: []string{"c1"}, Gens: map[string]int{"c1": 1}})
	assert.Len(t, p.Units, 1)
	entry := p.Units[0].Changes[0].Entry
	assert.Equal(t, "progress-value", entry.Set["stopped_progress"])
	assert.Contains(t, entry.Unset, FieldProgress)
}

// TestSprintStopReturnCoverMaxReason tests a reason longer than MaxCardTextBytes is cut.
func TestSprintStopReturnCoverMaxReason(t *testing.T) {
	t.Parallel()
	s := testSnap()
	longReason := strings.Repeat("x", MaxCardTextBytes+100)
	c1 := &Card{ID: "c1", Row: "row1", Col: Working, Rev: 1, Fields: map[string]string{
		"gen": "1", "stream": "s1",
	}}
	s.Fleet = NewTable(Fleet)
	s.Fleet.SetRows([]string{"row1"})
	s.Fleet.Put(c1)

	p := StopReturn(s, StopReturnReq{As: "row1", Reason: longReason, IDs: []string{"c1"}, Gens: map[string]int{"c1": 1}})
	assert.Len(t, p.Units, 1)
	entry := p.Units[0].Changes[0].Entry
	assert.LessOrEqual(t, len(entry.Set["stopped_reason"]), MaxCardTextBytes)
}

// TestSprintStopReturnCoverRepeat tests a card already returned answers Said.
func TestSprintStopReturnCoverRepeat(t *testing.T) {
	t.Parallel()
	s := testSnap()
	c1 := &Card{ID: "c1", Row: "row1", Col: Ready, Rev: 1, Fields: map[string]string{
		"gen": "2", "stopped_from_gen": "1", "stream": "s1",
	}}
	s.Fleet = NewTable(Fleet)
	s.Fleet.SetRows([]string{"row1"})
	s.Fleet.Put(c1)

	p := StopReturn(s, StopReturnReq{As: "row1", Reason: "cancel", IDs: []string{"c1"}, Gens: map[string]int{"c1": 1}})
	assert.Len(t, p.Said, 1)
	assert.Contains(t, p.Said[0], "was returned already")
	assert.Len(t, p.Units, 0)
	assert.Len(t, p.Refused, 0)
}

// TestSprintStopReturnCoverMainPath tests the main path: fleet card moves to ready.
func TestSprintStopReturnCoverMainPath(t *testing.T) {
	t.Parallel()
	s := testSnap()
	c1 := &Card{ID: "c1", Row: "row1", Col: Working, Rev: 1, Fields: map[string]string{
		"gen": "1", "stream": "s1",
	}}
	s.Fleet = NewTable(Fleet)
	s.Fleet.SetRows([]string{"row1"})
	s.Fleet.Put(c1)

	p := StopReturn(s, StopReturnReq{As: "row1", Reason: "cancel", IDs: []string{"c1"}, Gens: map[string]int{"c1": 1}})
	assert.Len(t, p.Units, 1)
	assert.Equal(t, "c1", p.Units[0].Key)
	assert.Equal(t, "s1", p.Units[0].Stream)
	assert.Contains(t, p.Units[0].Moved, "(cancel acknowledged)")
	assert.Len(t, p.Units[0].Changes, 1)
	entry := p.Units[0].Changes[0].Entry
	assert.Equal(t, Ready, entry.Move.Col)
	assert.Equal(t, "2", entry.Set["gen"])
	assert.Equal(t, "1", entry.Set["stopped_from_gen"])
	assert.Equal(t, "cancel", entry.Set["stopped_reason"])
	assert.Contains(t, entry.Set["untaken_since"], "2026-10-10")
	// Unsets only include fields that were present on the card; no assertions required.
}
