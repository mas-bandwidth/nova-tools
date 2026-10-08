package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoppedAssignmentsDistinguishLeasesFromNativeExecution(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, table, col string
		running, alert   bool
	}{
		{"stopped work lease", Fleet, Working, false, true},
		{"stopped read lease", Readers, Reading, false, true},
		{"stopped ready lease", Fleet, Ready, false, false},
		{"stopped completed lease", Fleet, DoneOK, false, false},
		{"running work expected", Fleet, Working, true, false},
		{"paused work expected", Fleet, Working, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			w.s.Running = tc.running
			tb := w.s.T(tc.table)
			tb.SetRows([]string{"owner"})
			card := &Card{ID: "c1", Row: "owner", Col: tc.col, Fields: map[string]string{"gen": "3"}}
			tb.Put(card)
			p := w.must(StoppedAssignments(w.s))
			if !tc.alert {
				assert.Empty(t, p.Props)
				assert.Empty(t, p.Units)
				return
			}
			ns := w.notesOf(NStoppedAssignments + ": owner")
			require.Len(t, ns, 1)
			assert.Equal(t, w.s.Coordinator, ns[0].To)
			assert.Contains(t, ns[0].What, "execution unknown / needs evidence")
			assert.Contains(t, ns[0].What, "c1@3")
			assert.NotContains(t, ns[0].What, "execution=running")
			assert.True(t, TickEndCounts(ns[0]))
			assert.Empty(t, p.Units[0].Changes, "an observation cannot return, claim or launch work")
			assert.Equal(t, tc.col, tb.Card("c1").Col)
			assert.Equal(t, 3, tb.Card("c1").Int("gen"))
			w.must(StoppedAssignments(w.s))
			assert.Len(t, w.notesOf(NStoppedAssignments+": owner"), 1, "persistent episode prevents repeat notifications")
		})
	}
}

func TestStoppedAssignmentEpisodeSurvivesRestartAndRecoversOnce(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.s.Fleet.SetRows([]string{"owner"})
	w.s.Fleet.Put(&Card{ID: "c1", Row: "owner", Col: Working, Fields: map[string]string{"gen": "1"}})
	w.must(StoppedAssignments(w.s))
	// Reconstructed planner state reads only canonical tables and their durable props.
	fresh := *w.s
	w.s = &fresh
	w.tick(time.Minute)
	w.must(StoppedAssignments(w.s))
	assert.Len(t, w.notesOf(NStoppedAssignments+": owner"), 1)
	w.s.Fleet.Put(&Card{ID: "c1", Row: "owner", Col: Ready, Fields: map[string]string{"gen": "2"}})
	w.must(StoppedAssignments(w.s))
	w.must(StoppedAssignments(w.s))
	assert.Len(t, w.notesOf(NStoppedAssignmentsCleared+": owner"), 1)
	w.s.Fleet.Put(&Card{ID: "c1", Row: "owner", Col: Working, Fields: map[string]string{"gen": "2"}})
	w.must(StoppedAssignments(w.s))
	assert.Len(t, w.notesOf(NStoppedAssignments+": owner"), 2, "a new unresolved episode is notified")
	w.s.Running = true
	w.must(StoppedAssignments(w.s))
	ns := w.notesOf(NStoppedAssignmentsCleared + ": owner")
	require.Len(t, ns, 2)
	assert.Contains(t, ns[1].What, "no conclusion about native execution")
}

func TestAnUnreadAssignmentTableDoesNotClearTheAlert(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.s.Fleet.SetProp(propStoppedAssignments+"owner", stamp(t0))
	w.s.Readers = nil
	p := StoppedAssignments(w.s)
	assert.Empty(t, p.Props)
	assert.Empty(t, p.Units)
}

func TestAnUnaddressedSTOPAlertLeavesTheEpisodeRetryable(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.s.Fleet.SetRows([]string{"owner"})
	w.s.Fleet.Put(&Card{ID: "c1", Row: "owner", Col: Working})
	w.s.Coordinator = ""
	p := StoppedAssignments(w.s)
	require.Len(t, p.Refused, 1)
	assert.Empty(t, p.Props)
	assert.Empty(t, p.Units)
	p, due := TickStoppedAssignments(w.s, TickReq{})
	assert.NotEmpty(t, p.Refused)
	assert.Equal(t, 1, due, "the tick must not pass a missing recipient over")
	w.s.Coordinator = "coordinator"
	w.must(StoppedAssignments(w.s))
	assert.Len(t, w.notesOf(NStoppedAssignments+": owner"), 1)
}

func TestSTOPAlertsKeepEveryOwnersEvidenceThroughInboxGrouping(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.s.Fleet.SetRows([]string{"owner-a", "owner-b"})
	for _, row := range w.s.Fleet.Rows() {
		w.s.Fleet.Put(&Card{ID: row + "-card", Row: row, Col: Working})
	}
	p := w.must(StoppedAssignments(w.s))
	var notes []Note
	for _, u := range p.Units {
		notes = append(notes, u.Notes...)
	}
	groups := Inbox(InboxReq{Recent: notes, Now: t0})
	require.Len(t, groups, 2)
	for _, row := range w.s.Fleet.Rows() {
		found := false
		for _, g := range groups {
			if g.Type == NStoppedAssignments+": "+row {
				found = true
				assert.Contains(t, g.What, row+"-card")
			}
		}
		assert.True(t, found, row)
	}
}

func TestAPausedSnapshotOnlyClearsAnEarlierSTOPIncident(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.s.Fleet.SetRows([]string{"owner"})
	w.s.Fleet.Put(&Card{ID: "c1", Row: "owner", Col: Working, Fields: map[string]string{"gen": "4"}})
	w.must(StoppedAssignments(w.s))
	// Pause preserves the fenced running state and canonical work unchanged.
	w.s.Running = true
	p, due := TickStoppedAssignments(w.s, TickReq{})
	w.must(p)
	assert.Zero(t, due)
	assert.Len(t, w.notesOf(NStoppedAssignments+": owner"), 1)
	assert.Len(t, w.notesOf(NStoppedAssignmentsCleared+": owner"), 1)
	assert.Equal(t, Working, w.s.Fleet.Card("c1").Col)
	assert.Equal(t, 4, w.s.Fleet.Card("c1").Int("gen"))
	p, _ = TickStoppedAssignments(w.s, TickReq{})
	assert.True(t, p.Empty())
}
