package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Timers on the twin store with the harness's fake clock (docs/SPEC-SPRINT.md,
// "Timers"; tla/Timers.tla: NeverEarly, AtMostOnce, EndsOnce, MissedListed).

func (h *harness) setTimer(actor, note string, in, within time.Duration) sprint.Timer {
	h.t.Helper()
	t, err := h.st.SetTimer(h.ctx, sprint.Timer{Actor: actor, Setter: h.st.Actor, Note: note, Due: h.st.now().Add(in), Within: within}, false)
	require.NoError(h.t, err)
	return t
}

// timerNotes is every note of the timer types the store holds, oldest first.
func (h *harness) timerNotes() []sprint.Note {
	h.t.Helper()
	notes, _, err := h.m.NotesSince(h.ctx, "", 10000)
	require.NoError(h.t, err)
	var out []sprint.Note
	for _, n := range notes {
		switch n.Type {
		case sprint.NTimer, sprint.NTimerFired, sprint.NTimerExpired:
			if n.Kind == sprint.Decided || n.Kind == sprint.Acknowledged {
				continue // the answer of a judgment, not a timer's note
			}
			out = append(out, n)
		}
	}
	return out
}

// timerJudgments is the open timer judgments of the inbox.
func (h *harness) timerJudgments() []sprint.Group {
	h.t.Helper()
	v, err := h.st.Inbox(h.ctx, time.Hour, time.Hour, 10000)
	require.NoError(h.t, err)
	var out []sprint.Group
	for _, g := range v.Groups {
		if g.Type == sprint.NTimer && g.Kind == sprint.Judgment {
			out = append(out, g)
		}
	}
	return out
}

func (h *harness) timerState(id string) (string, sprint.TimerEnd) {
	h.t.Helper()
	b, err := h.st.TimerBook(h.ctx)
	require.NoError(h.t, err)
	i := b.Timers.Find(id)
	require.GreaterOrEqual(h.t, i, 0, "timer %s", id)
	return sprint.TimerState(b.Timers.All[i], b.End(id)), b.End(id)
}

func (h *harness) missed() []string {
	h.t.Helper()
	b, err := h.st.TimerBook(h.ctx)
	require.NoError(h.t, err)
	var out []string
	for _, t := range b.Timers.All {
		if sprint.Missed(t, b.End(t.ID)) {
			out = append(out, t.ID)
		}
	}
	return out
}

func TestTimerFiresOnceAtItsDueTimeNeverEarly(t *testing.T) {
	t.Parallel()
	for _, running := range []bool{true, false} {
		t.Run(map[bool]string{true: "running", false: "stopped"}[running], func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			if running {
				h.startMachine()
			}
			tm := h.setTimer(h.st.Actor, "look at the lander", 2*time.Minute, time.Hour)
			assert.Equal(t, "t1", tm.ID)
			h.tick(2*time.Minute - time.Second)
			h.machine()
			state, _ := h.timerState(tm.ID)
			assert.Equal(t, sprint.TimerPending, state, "a second before its due time")
			assert.Empty(t, h.timerNotes(), "a second before its due time")
			h.tick(time.Second)
			h.machine()
			state, end := h.timerState(tm.ID)
			assert.Equal(t, sprint.TimerFired, state)
			assert.True(t, end.Fired.Equal(tm.Due), "fired at its due time: %v", end.Fired)
			assert.True(t, end.Judged, "the seat's holder's timer is a judgment")
			for i := 0; i < 5; i++ { // exactly once: later ticks raise nothing more
				h.tick(time.Minute)
				h.machine()
			}
			notes := h.timerNotes()
			require.Len(t, notes, 1, "one judgment: %+v", notes)
			assert.Equal(t, sprint.Judgment, notes[0].Kind)
			assert.Contains(t, notes[0].What, "look at the lander")
			assert.Contains(t, notes[0].What, "0s late")
			assert.Len(t, h.timerJudgments(), 1, "open on the inbox")
		})
	}
}

func TestTimerLateFireShowsItsLateness(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	tm := h.setTimer(h.st.Actor, "late one", time.Minute, time.Hour)
	h.tick(31 * time.Minute) // no tick ran from its due time until now: the server was down
	h.machine()
	state, end := h.timerState(tm.ID)
	require.Equal(t, sprint.TimerFired, state)
	assert.Equal(t, 30*time.Minute, end.Lateness(tm))
	notes := h.timerNotes()
	require.Len(t, notes, 1)
	assert.Contains(t, notes[0].What, "30m0s late")
}

func TestTimerExpiresWhenTheServerWasDownPastItsWindow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	tm := h.setTimer(h.st.Actor, "missed window", time.Minute, time.Hour)
	h.tick(time.Minute + time.Hour + time.Second)
	h.machine()
	state, end := h.timerState(tm.ID)
	require.Equal(t, sprint.TimerExpired, state)
	assert.True(t, end.Fired.IsZero(), "never fired")
	assert.Contains(t, end.Reason, "no tick ran")
	notes := h.timerNotes()
	require.Len(t, notes, 1, "the setter is told, once: %+v", notes)
	assert.Equal(t, sprint.NTimerExpired, notes[0].Type)
	assert.Equal(t, h.st.Actor, notes[0].To)
	assert.Empty(t, h.timerJudgments(), "an expired timer raises no judgment")
	assert.Equal(t, []string{tm.ID}, h.missed())
	h.tick(time.Minute)
	h.machine()
	assert.Len(t, h.timerNotes(), 1, "told once")
}

func TestTimerUnseenIsMissedThenExpiresAndAckClearsIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	tm := h.setTimer(h.st.Actor, "one hour", time.Hour, time.Hour)
	h.tick(time.Hour)
	h.machine()
	h.tick(30 * time.Minute) // nobody acknowledges
	h.machine()
	state, _ := h.timerState(tm.ID)
	assert.Equal(t, sprint.TimerFired, state)
	assert.Equal(t, []string{tm.ID}, h.missed(), "fired and unseen is missed")
	h.tick(30*time.Minute + time.Second) // two hours on: the window closed unseen
	h.machine()
	state, end := h.timerState(tm.ID)
	require.Equal(t, sprint.TimerExpired, state)
	assert.False(t, end.Fired.IsZero(), "it fired before it expired")
	assert.Contains(t, end.Reason, "not seen")
	assert.Equal(t, []string{tm.ID}, h.missed(), "expired unseen is missed")
	assert.Empty(t, h.timerJudgments(), "its judgment closed")
	_, _, changed, err := h.st.AckTimer(h.ctx, tm.ID, false)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Empty(t, h.missed(), "acknowledged: off the missed list")
	state, _ = h.timerState(tm.ID)
	assert.Equal(t, sprint.TimerExpired, state, "still expired: one end")
}

func TestTimerAckOrAnswerMakesItSeen(t *testing.T) {
	t.Parallel()
	for _, how := range []string{"remind --ack", "ack its judgment"} {
		t.Run(how, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			tm := h.setTimer(h.st.Actor, "see me", time.Minute, time.Hour)
			h.tick(time.Minute)
			h.machine()
			js := h.timerJudgments()
			require.Len(t, js, 1)
			if how == "remind --ack" {
				_, _, _, err := h.st.AckTimer(h.ctx, tm.ID, false)
				require.NoError(t, err)
			} else {
				h.must(AckStep(sprint.AckReq{Notes: js[0].Notes, Reason: "seen", Who: h.st.Actor}))
			}
			h.tick(time.Second)
			h.machine()
			state, _ := h.timerState(tm.ID)
			assert.Equal(t, sprint.TimerSeen, state)
			assert.Empty(t, h.missed())
			assert.Empty(t, h.timerJudgments(), "the judgment closed")
			h.tick(2 * time.Hour) // a seen timer never expires
			h.machine()
			state, _ = h.timerState(tm.ID)
			assert.Equal(t, sprint.TimerSeen, state)
			assert.Len(t, h.timerNotes(), 1, "nothing more was written")
		})
	}
}

func TestTimerCancelBeforeItFires(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	tm := h.setTimer(h.st.Actor, "never mind", time.Minute, time.Hour)
	_, err := h.st.CancelTimer(h.ctx, tm.ID, true)
	require.NoError(t, err)
	state, _ := h.timerState(tm.ID)
	require.Equal(t, sprint.TimerPending, state, "a dry run writes nothing")
	_, err = h.st.CancelTimer(h.ctx, tm.ID, false)
	require.NoError(t, err)
	h.tick(time.Hour)
	h.machine()
	state, _ = h.timerState(tm.ID)
	assert.Equal(t, sprint.TimerCancelled, state)
	assert.Empty(t, h.timerNotes())
	_, err = h.st.CancelTimer(h.ctx, tm.ID, false)
	assert.ErrorIs(t, err, ErrTimer, "a cancelled timer is not cancelled again")
	fired := h.setTimer(h.st.Actor, "fired", time.Minute, time.Hour)
	h.tick(time.Minute)
	h.machine()
	_, err = h.st.CancelTimer(h.ctx, fired.ID, false)
	assert.ErrorIs(t, err, ErrTimer, "a fired timer is not cancelled")
}

func TestTimerSurvivesARestartOfTheServer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	tm := h.setTimer(h.st.Actor, "after the restart", 10*time.Minute, time.Hour)
	// the server stops: a new process opens the same store with its own state
	h.tick(10 * time.Minute)
	restarted := &Store{B: h.m, Names: h.st.Names, Actor: h.st.Actor, Now: h.st.Now, NewID: h.st.NewID, Sleep: h.st.Sleep}
	_, err := restarted.Tick(h.ctx)
	require.NoError(t, err)
	state, _ := h.timerState(tm.ID)
	assert.Equal(t, sprint.TimerFired, state)
	_, err = restarted.Tick(h.ctx)
	require.NoError(t, err)
	h.machine() // and the old one, ticking too, fires nothing again
	assert.Len(t, h.timerNotes(), 1, "fired once across the restart")
}

func TestTimerForAFriendIsANoteToHerAndTheSetterIsTold(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2}})
	require.NoError(t, err)
	tm := h.setTimer("amy", "check the build", time.Minute, time.Hour)
	h.tick(time.Minute)
	h.machine()
	notes := h.timerNotes()
	require.Len(t, notes, 2, "%+v", notes)
	to := map[string]string{}
	for _, n := range notes {
		assert.Equal(t, sprint.Happened, n.Kind)
		to[n.To] = n.Type
	}
	assert.Equal(t, map[string]string{"amy": sprint.NTimerFired, h.st.Actor: sprint.NTimerFired}, to)
	assert.Empty(t, h.timerJudgments(), "a friend's timer is no judgment of the seat")
	_, end := h.timerState(tm.ID)
	assert.False(t, end.Judged)
	assert.Empty(t, end.Owed, "the notes owed were written")
}

func TestTimerRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for name, tm := range map[string]sprint.Timer{
		"no note":         {Actor: h.st.Actor, Due: t0.Add(time.Minute)},
		"due now":         {Actor: h.st.Actor, Note: "x", Due: t0},
		"unknown actor":   {Actor: "nobody", Note: "x", Due: t0.Add(time.Minute)},
		"due in the past": {Actor: h.st.Actor, Note: "x", Due: t0.Add(-time.Minute)},
	} {
		_, err := h.st.SetTimer(h.ctx, tm, false)
		assert.ErrorIs(t, err, ErrTimer, name)
	}
	_, _, _, err := h.st.AckTimer(h.ctx, "t9", false)
	assert.ErrorIs(t, err, ErrTimer, "no such timer")
	pending := h.setTimer(h.st.Actor, "pending", time.Hour, time.Hour)
	_, _, _, err = h.st.AckTimer(h.ctx, pending.ID, false)
	assert.ErrorIs(t, err, ErrTimer, "a pending timer is not acked")
	_, err = h.st.SetTimer(h.ctx, sprint.Timer{Actor: h.st.Actor, Note: "dry", Due: t0.Add(time.Hour)}, true)
	require.NoError(t, err)
	b, err := h.st.TimerBook(h.ctx)
	require.NoError(t, err)
	assert.Len(t, b.Timers.All, 1, "a dry run writes nothing")
}

func TestTimerActorLeftTheSprintExpires(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "amy", Width: 2}})
	require.NoError(t, err)
	tm := h.setTimer("amy", "gone", time.Minute, time.Hour)
	_, _, _, err = h.st.SyncFriends(h.ctx, nil)
	require.NoError(t, err)
	h.tick(time.Minute)
	h.machine()
	state, end := h.timerState(tm.ID)
	require.Equal(t, sprint.TimerExpired, state)
	assert.Contains(t, end.Reason, "not in the sprint")
}

// overwritingKV is the twin with another remind verb's write landing right
// after this one's first write of the timers record.
type overwritingKV struct {
	*Mem
	other string // the record the other verb writes, once
}

func (o *overwritingKV) SetKey(ctx context.Context, name, value string) error {
	if err := o.Mem.SetKey(ctx, name, value); err != nil || name != keyTimers || o.other == "" {
		return err
	}
	other := o.other
	o.other = ""
	return o.Mem.SetKey(ctx, name, other)
}

func TestTimerSetOverwrittenByAnotherVerbIsMadeAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	first := h.setTimer(h.st.Actor, "first", time.Hour, time.Hour)
	b, err := h.st.TimerBook(h.ctx)
	require.NoError(t, err)
	// the other verb read the record before this one wrote, and writes it back as it read it
	other, err := json.Marshal(b.Timers)
	require.NoError(t, err)
	st := *h.st
	st.B = &overwritingKV{Mem: h.m, other: string(other)}
	second, err := st.SetTimer(h.ctx, sprint.Timer{Actor: h.st.Actor, Setter: h.st.Actor, Note: "second", Due: t0.Add(time.Hour)}, false)
	require.NoError(t, err)
	b, err = h.st.TimerBook(h.ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, b.Timers.Find(first.ID), 0, "the first stands")
	assert.GreaterOrEqual(t, b.Timers.Find(second.ID), 0, "the overwritten set was made again: %+v", b.Timers)
}
