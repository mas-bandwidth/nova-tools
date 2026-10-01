package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// failFleetRead makes the tick's read of the fleet table fail with text, the
// real case of 2026-10-01: a fleet table that lacks a column. The steps that
// write notes read other tables and still work.
func (h *harness) failFleetRead(text string) {
	h.m.Fail = func(p string) error {
		if p == "readset "+h.st.Names.Table(sprint.Fleet) {
			return errors.New(text)
		}
		return nil
	}
}

// failedTicks runs n failed ticks, a second apart.
func (h *harness) failedTicks(n int) {
	h.t.Helper()
	for i := 0; i < n; i++ {
		h.tick(time.Second)
		_, err := h.st.Tick(h.ctx)
		require.Error(h.t, err)
	}
}

// tickNotes are the notes of one type, in order.
func (h *harness) tickNotes(typ string) []sprint.Note {
	h.t.Helper()
	notes, _, err := h.m.NotesSince(h.ctx, "", 100000)
	require.NoError(h.t, err)
	var out []sprint.Note
	for _, n := range notes {
		if n.Type == typ {
			out = append(out, n)
		}
	}
	return out
}

// A failed tick is a judgment (docs/SPEC-SPRINT.md section 14, the tick's
// failure): three failed ticks with one error text leave exactly one note,
// naming the error, the first failed tick's number and the time, addressed to the
// coordinator; the log line and the sprint line are unchanged.
func TestThreeFailedTicksWithOneErrorLeaveOneNote(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	h.failFleetRead("fleet: no such column")
	h.failedTicks(3)
	notes := h.tickNotes(sprint.NTickFailed)
	require.Len(t, notes, 1)
	n := notes[0]
	require.Equal(t, sprint.Happened, n.Kind)
	require.Equal(t, h.st.Actor, n.To)
	require.Contains(t, n.What, "fleet: no such column")
	require.Contains(t, n.What, "tick 2 failed at "+h.now.Add(-2*time.Second).UTC().Format(time.RFC3339))
	require.Contains(t, h.st.MachineLine(h.ctx), "last tick failed: ")
}

// A different error text is a different failure: one more note, and the same
// text again after it is not written again.
func TestADifferentErrorTextAddsOneNote(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	h.failFleetRead("first text")
	h.failedTicks(3)
	h.failFleetRead("second text")
	h.failedTicks(2)
	notes := h.tickNotes(sprint.NTickFailed)
	require.Len(t, notes, 2)
	require.Contains(t, notes[0].What, "first text")
	require.Contains(t, notes[1].What, "second text")
	require.Contains(t, notes[1].What, "tick 5 failed")
}

// A tick that works again after failing says so once, with the count of
// failed ticks; a tick after that says nothing, and a failure after the
// recovery is a new failure with its own note.
func TestRecoveryAddsOneNoteWithTheFailedCount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	h.failFleetRead("fleet: no such column")
	h.failedTicks(3)
	h.m.Fail = nil
	h.tick(time.Second)
	h.machine()
	h.tick(time.Second)
	h.machine()
	notes := h.tickNotes(sprint.NTickRecovered)
	require.Len(t, notes, 1)
	require.Equal(t, sprint.Happened, notes[0].Kind)
	require.Equal(t, h.st.Actor, notes[0].To)
	require.Contains(t, notes[0].What, "failed=3")
	h.failFleetRead("fleet: no such column")
	h.failedTicks(1)
	require.Len(t, h.tickNotes(sprint.NTickFailed), 2)
}

// The notes ride the inbox as any note to the coordinator does, and
// inbox --wait wakes on the first: a wait from before the failure returns
// when the first failed tick has written its note and its tick end.
func TestTickFailureNotesWakeInboxWait(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	_, from, err := h.m.Tails(h.ctx)
	require.NoError(t, err)
	h.failFleetRead("fleet: no such column")
	h.failedTicks(1)
	waiter := &Store{B: h.m, Names: h.st.Names, Actor: h.st.Actor, Now: h.st.Now, NewID: h.st.NewID, Sleep: h.tick}
	woke, err := waiter.WaitTickEnd(h.ctx, from, time.Minute)
	require.NoError(t, err)
	require.True(t, woke)
	h.failedTicks(2)
	require.Equal(t, 1, h.written(sprint.NTickEnd))
	v, err := h.st.Inbox(h.ctx, time.Hour, time.Hour, 1000)
	require.NoError(t, err)
	var found *sprint.Group
	for i, g := range v.Groups {
		if g.Type == sprint.NTickFailed {
			found = &v.Groups[i]
		}
	}
	require.NotNil(t, found, fmt.Sprint(v.Groups))
	require.Equal(t, h.st.Actor, found.To)
	require.True(t, strings.Contains(found.What, "no such column"))
}
