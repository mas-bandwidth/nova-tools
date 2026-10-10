package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Unit coverage for StopReturn (internal/sprint/stop_return.go): a pure planner
// that reads a *Snapshot and a StopReturnReq and answers a Plan. All pure:
// hand-made tables, with no store, clock, sleep, subprocess, network or server.

func stopReturnPreFleet(row string, ids ...string) *Snapshot {
	s := &Snapshot{Epoch: 1, Now: time.Unix(1234567890, 0), Fleet: NewTable(Fleet)}
	s.Fleet.SetRows([]string{row})
	for i, id := range ids {
		s.Fleet.Put(&Card{ID: id, Row: row, Col: Working, Score: int64(i + 1), Rev: 1,
			Fields: map[string]string{"gen": itoa(i + 1), "stream": id + "-stream"}})
	}
	return s
}

func stopReturnPreReaders(row string, ids ...string) *Snapshot {
	s := &Snapshot{Epoch: 1, Now: time.Unix(1234567890, 0), Readers: NewTable(Readers)}
	s.Readers.SetRows([]string{row})
	for i, id := range ids {
		s.Readers.Put(&Card{ID: id, Row: row, Col: Reading, Score: int64(i + 1), Rev: 1,
			Fields: map[string]string{"stream": id + "-stream"}})
	}
	return s
}

func TestSprintStopReturnCoverFleetCardMovesToReady(t *testing.T) {
	t.Parallel()
	s := stopReturnPreFleet("r1", "c1")
	p := StopReturn(s, StopReturnReq{As: "r1", IDs: []string{"c1"}, Gens: map[string]int{"c1": 1}, Reason: "done"})
	require.Len(t, p.Units, 1)
	u := p.Units[0]
	require.Equal(t, "c1", u.Key)
	require.Equal(t, "c1-stream", u.Stream)
	require.Len(t, u.Changes, 1)
	e := u.Changes[0].Entry
	require.Equal(t, "c1", e.ID)
	require.Equal(t, "r1", e.Expect.Place.Row)
	require.Equal(t, Ready, e.Expect.Place.Col)
	require.Equal(t, "2", e.Set["gen"])
	require.Equal(t, "1", e.Set["stopped_from_gen"])
	require.Equal(t, "done", e.Set["stopped_reason"])
	require.NotEmpty(t, e.Set["untaken_since"])
	require.Contains(t, e.Unset, "taken")
	require.Contains(t, e.Unset, "begun")
	require.Contains(t, e.Unset, FieldStarted)
	require.Contains(t, e.Unset, FieldFriendDeadline)
	require.Contains(t, u.Moved, "(cancel acknowledged)")
	require.Empty(t, p.Refused)
	require.Empty(t, p.Said)
}

func TestSprintStopReturnCoverCardWithProgress(t *testing.T) {
	t.Parallel()
	s := stopReturnPreFleet("r1", "c1")
	s.Fleet.Put(&Card{ID: "c1", Row: "r1", Col: Working, Score: 1, Rev: 1,
		Fields: map[string]string{"gen": "1", "stream": "c1-stream", FieldProgress: "fixing bug"}})
	p := StopReturn(s, StopReturnReq{As: "r1", IDs: []string{"c1"}, Gens: map[string]int{"c1": 1}, Reason: "done"})
	require.Len(t, p.Units, 1)
	require.Equal(t, "fixing bug", p.Units[0].Changes[0].Entry.Set["stopped_progress"])
	require.Contains(t, p.Units[0].Changes[0].Entry.Unset, FieldProgress)
}

func TestSprintStopReturnCoverReaderCardMovesToAsked(t *testing.T) {
	t.Parallel()
	s := stopReturnPreReaders("r1", "c1")
	p := StopReturn(s, StopReturnReq{As: "r1", IDs: []string{"c1"}, Gens: map[string]int{"c1": 1}, Reason: "done"})
	require.Len(t, p.Units, 1)
	u := p.Units[0]
	require.Equal(t, "c1", u.Key)
	require.Equal(t, "c1-stream", u.Stream)
	require.Len(t, u.Changes, 1)
	e := u.Changes[0].Entry
	require.Equal(t, "r1", e.Expect.Place.Row)
	require.Equal(t, Asked, e.Expect.Place.Col)
	require.Contains(t, u.Moved, "(cancel acknowledged)")
}

func TestSprintStopReturnCoverNoGenCountsAsOne(t *testing.T) {
	t.Parallel()
	s := stopReturnPreFleet("r1", "c1")
	s.Fleet.Put(&Card{ID: "c1", Row: "r1", Col: Working, Score: 1, Rev: 1,
		Fields: map[string]string{"stream": "c1-stream"}})
	p := StopReturn(s, StopReturnReq{As: "r1", IDs: []string{"c1"}, Reason: "done"})
	require.Len(t, p.Units, 1)
}

func TestSprintStopReturnCoverReasonCut(t *testing.T) {
	t.Parallel()
	s := stopReturnPreFleet("r1", "c1")
	longReason := strings.Repeat("x", MaxCardTextBytes+100)
	p := StopReturn(s, StopReturnReq{As: "r1", IDs: []string{"c1"}, Gens: map[string]int{"c1": 1}, Reason: longReason})
	require.Len(t, p.Units, 1)
	require.Len(t, p.Units[0].Changes[0].Entry.Set["stopped_reason"], MaxCardTextBytes)
}

func TestSprintStopReturnCoverRefusesBlankReason(t *testing.T) {
	t.Parallel()
	s := stopReturnPreFleet("r1", "c1")
	p := StopReturn(s, StopReturnReq{As: "r1", IDs: []string{"c1"}, Reason: ""})
	require.Len(t, p.Refused, 1)
	require.Equal(t, "stop-return", p.Refused[0].Key)
	require.Contains(t, p.Refused[0].Why, "name the observed cancellation acknowledgement")
	require.Empty(t, p.Units)
}

func TestSprintStopReturnCoverRefusesIDNamedTwice(t *testing.T) {
	t.Parallel()
	s := stopReturnPreFleet("r1", "c1")
	p := StopReturn(s, StopReturnReq{As: "r1", IDs: []string{"c1", "c1"}, Gens: map[string]int{"c1": 1}, Reason: "done"})
	require.Len(t, p.Refused, 1)
	require.Equal(t, "c1", p.Refused[0].Key)
	require.Contains(t, p.Refused[0].Why, "named twice")
	require.Len(t, p.Units, 1)
}

func TestSprintStopReturnCoverRefusesIDOnNoTable(t *testing.T) {
	t.Parallel()
	s := stopReturnPreFleet("r1", "c1")
	p := StopReturn(s, StopReturnReq{As: "r1", IDs: []string{"unknown"}, Gens: map[string]int{"unknown": 1}, Reason: "done"})
	require.Len(t, p.Refused, 1)
	require.Equal(t, "unknown", p.Refused[0].Key)
	require.Contains(t, p.Refused[0].Why, "not a live card on")
	require.Empty(t, p.Units)
}

func TestSprintStopReturnCoverRefusesIDOnAnotherRow(t *testing.T) {
	t.Parallel()
	s := stopReturnPreFleet("r1", "c1")
	p := StopReturn(s, StopReturnReq{As: "r2", IDs: []string{"c1"}, Gens: map[string]int{"c1": 1}, Reason: "done"})
	require.Len(t, p.Refused, 1)
	require.Equal(t, "c1", p.Refused[0].Key)
	require.Contains(t, p.Refused[0].Why, "not a live card on")
	require.Empty(t, p.Units)
}

func TestSprintStopReturnCoverRefusesGenMissingOrBelowOne(t *testing.T) {
	t.Parallel()
	s := stopReturnPreFleet("r1", "c1")
	p := StopReturn(s, StopReturnReq{As: "r1", IDs: []string{"c1"}, Gens: map[string]int{"c1": 0}, Reason: "done"})
	require.Len(t, p.Refused, 1)
	require.Equal(t, "c1", p.Refused[0].Key)
	require.Contains(t, p.Refused[0].Why, "name the generation cancelled")
	require.Empty(t, p.Units)
}

func TestSprintStopReturnCoverRefusesStaleGen(t *testing.T) {
	t.Parallel()
	s := stopReturnPreFleet("r1", "c1")
	s.Fleet.Put(&Card{ID: "c1", Row: "r1", Col: Working, Score: 1, Rev: 1,
		Fields: map[string]string{"gen": "2", "stream": "c1-stream"}})
	p := StopReturn(s, StopReturnReq{As: "r1", IDs: []string{"c1"}, Gens: map[string]int{"c1": 1}, Reason: "done"})
	require.Len(t, p.Refused, 1)
	require.Equal(t, "c1", p.Refused[0].Key)
	require.Contains(t, p.Refused[0].Why, "stale:")
	require.Empty(t, p.Units)
}

func TestSprintStopReturnCoverRefusesWrongColumn(t *testing.T) {
	t.Parallel()
	s := stopReturnPreFleet("r1", "c1")
	s.Fleet.Put(&Card{ID: "c1", Row: "r1", Col: Ready, Score: 1, Rev: 1,
		Fields: map[string]string{"gen": "1", "stream": "c1-stream"}})
	p := StopReturn(s, StopReturnReq{As: "r1", IDs: []string{"c1"}, Gens: map[string]int{"c1": 1}, Reason: "done"})
	require.Len(t, p.Refused, 1)
	require.Equal(t, "c1", p.Refused[0].Key)
	require.Contains(t, p.Refused[0].Why, "not in working")
	require.Empty(t, p.Units)
}

func TestSprintStopReturnCoverRepeatAlreadyReturned(t *testing.T) {
	t.Parallel()
	s := stopReturnPreFleet("r1", "c1")
	s.Fleet.Put(&Card{ID: "c1", Row: "r1", Col: Ready, Score: 1, Rev: 1,
		Fields: map[string]string{"gen": "2", "stream": "c1-stream", "stopped_from_gen": "1"}})
	p := StopReturn(s, StopReturnReq{As: "r1", IDs: []string{"c1"}, Gens: map[string]int{"c1": 1}, Reason: "done"})
	require.Len(t, p.Said, 1)
	require.Contains(t, p.Said[0], "was returned already")
	require.Empty(t, p.Units)
	require.Empty(t, p.Refused)
}
