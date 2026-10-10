package sprint_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/hostload"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The machine's automatic stops (internal/sprint/stops.go; tla/CoordinatorPass.tla, Kind
// "stop" and "behind"; the owner, 2026-10-10: "i still dislike these silent stops/failures"),
// on the twin store with a fake clock: each stop is a judgment while it holds, raised again
// every ten minutes, listed in the stops record with its age, and closed when it clears; the
// judgments late on the coordinator escalate past a wait.

// tickBeating moves the clock by d, beats the readers, the friends (each with a wake ping her
// session answered) and the members named alone, and runs one tick.
func (r *holdRig) tickBeating(d time.Duration, members ...string) {
	r.t.Helper()
	r.mu.Lock()
	r.now = r.now.Add(d)
	r.mu.Unlock()
	require.NoError(r.t, r.st.BeatReaders(r.ctx))
	zero := 0.0
	for _, m := range members {
		_, err := r.st.Beat(r.ctx, m, &zero, hostload.Source{})
		require.NoError(r.t, err)
	}
	for _, f := range []string{"amy", "bob"} {
		_, err := r.st.FriendBeat(r.ctx, f)
		require.NoError(r.t, err)
		_, _, _, err = r.st.FriendHealth(r.ctx, f, "coordinator", sprint.FriendHealth{State: sprint.Up, Seen: r.st.Now(), Generation: sprint.FirstSeatGeneration}, "")
		require.NoError(r.t, err)
	}
	_, err := r.st.Tick(r.ctx)
	require.NoError(r.t, err)
}

// openNote is the open judgment of the type on the subject, nil when none is.
func (r *holdRig) openNote(typ, subject string) *sprint.Note {
	for _, o := range r.snap().Open {
		if o.Note.Type == typ && o.Subject() == subject {
			n := o.Note
			return &n
		}
	}
	return nil
}

// allNotes is every note the sprint wrote.
func (r *holdRig) allNotes() []sprint.Note {
	r.t.Helper()
	notes, _, err := r.st.B.(*store.Mem).NotesSince(r.ctx, "", 100000)
	require.NoError(r.t, err)
	return notes
}

// countNotes is how many notes of the kind and type the sprint wrote whose what says word.
func (r *holdRig) countNotes(kind, typ, word string) int {
	n := 0
	for _, x := range r.allNotes() {
		if x.Kind == kind && x.Type == typ && strings.Contains(x.What, word) {
			n++
		}
	}
	return n
}

// keptStop is the stop of the kind on the subject in the stops record the tick wrote.
func (r *holdRig) keptStop(kind, subject string) (sprint.Stop, bool) {
	r.t.Helper()
	rec, ok, err := r.st.StopsKept(r.ctx)
	require.NoError(r.t, err)
	require.True(r.t, ok, "the tick keeps the stops record")
	for _, x := range rec.Stops {
		if x.Kind == kind && x.Subject == subject {
			return x, true
		}
	}
	return sprint.Stop{}, false
}

func TestAMemberDownTheCoordinatorDidNotHoldIsRaisedAgainUntilItIsHeld(t *testing.T) {
	t.Parallel()
	r := newHoldRig(t, 2, 0)
	r.tickBeating(time.Second, "m1", "m2")
	require.Equal(t, sprint.Up, r.snap().MemberCtl("m2").F("status"))

	// m2 stops beating: the presence part puts it down, and within the dwell nothing is raised
	r.tickBeating(time.Minute, "m1")
	require.Equal(t, sprint.Down, r.snap().MemberCtl("m2").F("status"))
	r.tickBeating(time.Minute, "m1")
	assert.Nil(t, r.openNote(sprint.NStopMemberDown, "m2"), "within the dwell a lapse is not a stop yet")

	// past the dwell: one judgment with the evidence, the effect and the undo verb, pushed
	r.tickBeating(90*time.Second, "m1")
	j := r.openNote(sprint.NStopMemberDown, "m2")
	require.NotNil(t, j, "a member down the coordinator did not hold is a judgment")
	assert.Contains(t, j.What, "member m2 is down and the coordinator did not hold it: its last beat was at 20")
	assert.Contains(t, j.What, "effect: its width 2 idles")
	assert.Contains(t, j.What, "undo: ")
	assert.Contains(t, j.Decisions, "fleet down m2")
	x, ok := r.keptStop(sprint.StopKindMemberDown, "m2")
	require.True(t, ok, "the stops record lists it")
	assert.False(t, x.Since.IsZero(), "with when it began")
	now, err := r.st.StopsNow(r.ctx)
	require.NoError(t, err)
	assert.Contains(t, kinds(now.Stops), sprint.StopKindMemberDown+" m2", "the doctor's count, from a fresh read, lists it too")

	// stable while the stop holds (cold reader B): ticks later the judgment's text and the
	// stops record are as they were, neither rewritten every tick (one tick first: the record
	// is counted from a tick's first read, which the judgment raised above changes once)
	r.tickBeating(5*time.Second, "m1")
	rec0, _, err := r.st.StopsKept(r.ctx)
	require.NoError(t, err)
	r.tickBeating(5*time.Second, "m1")
	r.tickBeating(5*time.Second, "m1")
	assert.Equal(t, j.What, r.openNote(sprint.NStopMemberDown, "m2").What, "the text changes only when the stop does")
	rec1, _, err := r.st.StopsKept(r.ctx)
	require.NoError(t, err)
	assert.Equal(t, rec0.At, rec1.At, "the stops record is not written again while nothing changed")

	// it holds: raised again after ten minutes of running time in the one digest of the stops,
	// the judgment itself not rewritten
	r.tickBeating(10*time.Minute, "m1")
	assert.Equal(t, 0, r.countNotes(sprint.Happened, sprint.NRaisedAgain, "member m2 is down"), "no line of its own")
	assert.Equal(t, 1, r.countNotes(sprint.Happened, sprint.NStopsDigest, "1 automatic stops still hold (1 member-down); the 1 oldest: "), "one digest")
	assert.Equal(t, 1, r.countNotes(sprint.Happened, sprint.NStopsDigest, "undo: start its member loop"), "naming the undo verb")
	r.tickBeating(time.Minute, "m1")
	assert.Equal(t, 1, r.countNotes(sprint.Happened, sprint.NStopsDigest, ""), "once a window")
	r.tickBeating(10*time.Minute, "m1")
	assert.Equal(t, 2, r.countNotes(sprint.Happened, sprint.NStopsDigest, ""), "and again the next window")
	assert.Equal(t, 1, r.countNotes(sprint.Judgment, sprint.NStopMemberDown, "m2"), "one judgment an episode")

	// the coordinator holds it off: the stop is hers now, and the judgment closes
	r.hold(sprint.HoldReq{Names: []string{"m2"}, Reason: "off for the night"})
	r.tickBeating(time.Second, "m1")
	assert.Nil(t, r.openNote(sprint.NStopMemberDown, "m2"), "a member the coordinator holds is no automatic stop")
	r.tickBeating(time.Second, "m1")
	_, ok = r.keptStop(sprint.StopKindMemberDown, "m2")
	assert.False(t, ok, "and the record no longer lists it")
}

func TestACardPinnedToAFriendWhoIsNotUpIsRaisedUntilUnpinned(t *testing.T) {
	t.Parallel()
	r := newHoldRig(t, 0, 0)
	r.tickBeating(time.Second, "m1", "m2")
	// amy is held, then a card pinned to her alone is added: no one else may take it
	r.hold(sprint.HoldReq{Names: []string{"amy"}, Reason: "on another job"})
	r.must(store.AddStep(sprint.AddReq{Stream: "p1", Cards: []sprint.CardAdd{{ID: "p1-1", Brief: friendsBrief("only friend amy")}}}))
	r.tickBeating(time.Second, "m1", "m2")
	r.tickBeating(time.Second, "m1", "m2")
	require.Equal(t, sprint.Ready, r.snap().Work.Card("p1-1").Col, "the pin waits ready")

	j := r.openNote(sprint.NStopPinWaits, "p1-1")
	require.NotNil(t, j, "a pin waiting on a friend who is not up is a judgment")
	assert.Contains(t, j.What, "p1-1 is pinned to friend amy alone and waits: she is held; effect: ")
	assert.Contains(t, j.What, "undo: nova-sprint unpin p1-1")
	_, ok := r.keptStop(sprint.StopKindPinWaits, "p1-1")
	assert.True(t, ok, "the stops record lists it")

	// unpinned, it is anyone's: the stop clears and its judgment closes
	r.must(store.UnpinStep(sprint.UnpinReq{IDs: []string{"p1-1"}, Reason: "anyone may do it", Who: "coordinator"}))
	r.tickBeating(time.Second, "m1", "m2")
	r.tickBeating(time.Second, "m1", "m2")
	assert.Nil(t, r.openNote(sprint.NStopPinWaits, "p1-1"), "unpinned, nothing waits on her")
}

func TestJudgmentsLateOnTheCoordinatorArePushedAndEscalatePastAWait(t *testing.T) {
	t.Parallel()
	r := newHoldRig(t, 1, 0)
	r.tickBeating(time.Second, "m1", "m2")
	// a machine card comes back failed: a judgment that waits on the coordinator
	wc := r.takeOne("m1")
	r.must(store.FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{wc}}, Gens: map[string]int{wc: r.snap().Fleet.Card(wc).Int("gen")}, Failed: true, Who: "m1"}))
	require.NotNil(t, r.openNote(sprint.NWorkFailed, "s1-1"))

	// past its deadline: the overdue line is addressed to the coordinator, so the push delivers it
	r.tickBeating(sprint.DeadlineJudgment+time.Minute, "m1", "m2")
	pushed := 0
	for _, n := range r.allNotes() {
		if n.Kind == sprint.Happened && n.Type == sprint.NOverdue {
			assert.Equal(t, "coordinator", n.To, "the overdue line is the coordinator's: pushed")
			pushed++
		}
	}
	require.Equal(t, 1, pushed, "one overdue line")

	// a pass later: the judgments late on the coordinator, at level 0
	r.tickBeating(sprint.PassEvery, "m1", "m2")
	behind := sprint.StreamSubject("")
	b0 := r.openNote(sprint.NCoordinatorBehind, behind)
	require.NotNil(t, b0)
	assert.True(t, strings.HasPrefix(b0.What, "level 0: 1 judgments wait past their deadline, the oldest"), b0.What)

	// the coordinator waits it for two hours: quiet within its level
	_, _, err := r.st.Wait(r.ctx, b0.ID, r.st.Now().Add(2*time.Hour))
	require.NoError(t, err)
	before := r.countNotes(sprint.Happened, sprint.NRaisedAgain, "level 0")
	r.tickBeating(sprint.PassEvery, "m1", "m2")
	assert.Equal(t, before, r.countNotes(sprint.Happened, sprint.NRaisedAgain, "level 0"), "a wait quiets its own level")

	// the wait on the seat grows past the next level: a new judgment, whatever quieted the one before
	r.tickBeating(sprint.BehindEscalateEvery, "m1", "m2")
	b1 := r.openNote(sprint.NCoordinatorBehind, behind)
	require.NotNil(t, b1, "a new level is raised past the wait (EscalatedPastAck)")
	assert.NotEqual(t, b0.ID, b1.ID)
	assert.True(t, strings.HasPrefix(b1.What, "level 1: "), b1.What)

	// and the record (counted from the next tick's first read) carries what waits on the
	// seat, with the level
	r.tickBeating(time.Second, "m1", "m2")
	rec, ok, err := r.st.StopsKept(r.ctx)
	require.NoError(t, err)
	require.True(t, ok)
	assert.GreaterOrEqual(t, rec.Seat.Judgments, 1)
	assert.GreaterOrEqual(t, rec.Seat.Overdue, 1)
	assert.False(t, rec.Seat.OldestAt.IsZero())
	assert.Equal(t, 1, rec.Seat.Level)
}

// kinds is each stop as "<kind> <subject>".
func kinds(stops []sprint.Stop) []string {
	var out []string
	for _, x := range stops {
		out = append(out, x.Kind+" "+x.Subject)
	}
	return out
}
