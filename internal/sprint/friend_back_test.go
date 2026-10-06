package sprint_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend held or down for a cause that ends is probed and brought back up
// (docs/SPEC-SPRINT.md section 1, "A friend brought back up"). The clock is
// the rig's. Nothing sleeps, and no daemon is started.

func TestAFriendHeldForACauseThatEndedIsBroughtBackUp(t *testing.T) {
	t.Parallel()

	t.Run("down until comes up at that time after a passing probe", func(t *testing.T) {
		t.Parallel()
		r := newHoldRig(t, 0, 0)
		pass(r)
		until := holdT0.Add(30 * time.Second)
		require.NoError(t, r.st.SetFriendHeld(r.ctx, "amy", true, "coordinator", "", until, 0))
		r.tickAt(until.Add(-time.Second))
		assert.Equal(t, sprint.Held, r.friendStatus("amy"), "before the until, the backoff has not come due")
		assert.Empty(t, r.backNotes())
		r.tickAt(until)
		assert.Equal(t, sprint.Up, r.friendStatus("amy"))
		assert.Equal(t, 2, r.friendWidth("amy"), "her nova-config width")
		assert.Equal(t, []string{"amy is back up: until " + until.UTC().Format(time.RFC3339) + " ended"}, r.backNotes())
		r.tickAt(until.Add(time.Second))
		assert.Equal(t, sprint.Up, r.friendStatus("amy"))
		assert.Len(t, r.backNotes(), 1, "one note for the return")
	})

	t.Run("a beat that says down comes up at its until", func(t *testing.T) {
		t.Parallel()
		r := newHoldRig(t, 0, 0)
		pass(r)
		r.at(holdT0.Add(time.Second))
		until := r.st.Now().Add(30 * time.Second)
		zero := 0
		_, err := r.st.FriendBeatReport(r.ctx, "amy", sprint.FriendReport{Until: until, Reason: "usage limit", Working: &zero}, nil)
		require.NoError(t, err)
		assert.Equal(t, sprint.Down, r.friendStatus("amy"))
		r.tickNoBeat(until.Add(-time.Second))
		assert.Equal(t, sprint.Down, r.friendStatus("amy"))
		r.tickNoBeat(until)
		assert.Equal(t, sprint.Up, r.friendStatus("amy"))
		assert.Equal(t, []string{"amy is back up: usage limit ended"}, r.backNotes())
	})

	t.Run("out of credit is released when the probe passes and kept when it fails", func(t *testing.T) {
		t.Parallel()
		r := newHoldRig(t, 0, 0)
		var refuse bool
		r.ctx = sprint.WithFriendProbe(r.ctx, func(string) (sprint.FriendProbe, bool) {
			return sprint.FriendProbe{Session: true, FundsRefuse: refuse}, true
		})
		require.NoError(t, r.st.SetFriendHeld(r.ctx, "amy", true, "coordinator", "out of credits", time.Time{}, 0))
		r.tickAt(holdT0.Add(30 * time.Second))
		assert.Equal(t, sprint.Held, r.friendStatus("amy"), "the first probe waits a minute")
		refuse = true
		r.tickAt(holdT0.Add(sprint.FriendBackBase))
		assert.Equal(t, sprint.Held, r.friendStatus("amy"), "her next run would still refuse for funds")
		assert.Empty(t, r.backNotes())
		refuse = false
		r.tickAt(holdT0.Add(sprint.FriendBackBase + time.Second))
		assert.Equal(t, sprint.Held, r.friendStatus("amy"), "a failed probe backs off")
		r.tickAt(holdT0.Add(2 * sprint.FriendBackBase))
		assert.Equal(t, sprint.Up, r.friendStatus("amy"))
		assert.Equal(t, []string{"amy is back up: out of credit ended"}, r.backNotes())
	})

	t.Run("a daemon pong does not bring her up", func(t *testing.T) {
		t.Parallel()
		r := newHoldRig(t, 0, 0)
		var daemon bool
		r.ctx = sprint.WithFriendProbe(r.ctx, func(string) (sprint.FriendProbe, bool) {
			if daemon {
				return sprint.FriendProbe{Daemon: true}, true
			}
			return sprint.FriendProbe{Session: true}, true
		})
		until := holdT0.Add(time.Minute)
		require.NoError(t, r.st.SetFriendHeld(r.ctx, "amy", true, "coordinator", "deaf", until, 0))
		daemon = true
		r.tickAt(until)
		assert.Equal(t, sprint.Held, r.friendStatus("amy"), "her daemon answering is not her session")
		assert.Empty(t, r.backNotes())
		daemon = false
		r.tickAt(until)
		assert.Equal(t, sprint.Held, r.friendStatus("amy"), "the probe at the end was already made")
		r.tickAt(until.Add(sprint.FriendBackBase))
		assert.Equal(t, sprint.Up, r.friendStatus("amy"))
		assert.Equal(t, []string{"amy is back up: deaf ended"}, r.backNotes())
	})

	t.Run("a hand hold is never released", func(t *testing.T) {
		t.Parallel()
		r := newHoldRig(t, 0, 0)
		var called int
		r.ctx = sprint.WithFriendProbe(r.ctx, func(string) (sprint.FriendProbe, bool) {
			called++
			return sprint.FriendProbe{Session: true}, true
		})
		require.NoError(t, r.st.SetFriendHeld(r.ctx, "amy", true, "coordinator", "resting her", time.Time{}, 0))
		require.NoError(t, r.st.SetFriendHeld(r.ctx, "bob", true, "coordinator", "", time.Time{}, 0))
		r.tickAt(holdT0.Add(2 * time.Hour))
		assert.Equal(t, sprint.Held, r.friendStatus("amy"))
		assert.Equal(t, sprint.Held, r.friendStatus("bob"))
		assert.Zero(t, called, "a hold by hand is not probed")
		assert.Empty(t, r.backNotes())
	})

	t.Run("one note per friend, and a second tick adds none", func(t *testing.T) {
		t.Parallel()
		r := newHoldRig(t, 0, 0)
		pass(r)
		until := holdT0.Add(30 * time.Second)
		require.NoError(t, r.st.SetFriendHeld(r.ctx, "amy", true, "coordinator", "deaf", until, 0))
		require.NoError(t, r.st.SetFriendHeld(r.ctx, "bob", true, "coordinator", "out of credit", until, 0))
		r.tickAt(until)
		assert.Equal(t, sprint.Up, r.friendStatus("amy"))
		assert.Equal(t, sprint.Up, r.friendStatus("bob"))
		assert.ElementsMatch(t, []string{
			"amy is back up: deaf ended",
			"bob is back up: out of credit ended",
		}, r.backNotes())
		r.tickAt(until.Add(time.Second))
		assert.Len(t, r.backNotes(), 2)
	})

	t.Run("she comes up at her row width and her queue is levelled the same tick", func(t *testing.T) {
		t.Parallel()
		r := newHoldRig(t, 0, 0)
		pass(r)
		until := holdT0.Add(20 * time.Second)
		require.NoError(t, r.st.SetFriendHeld(r.ctx, "amy", true, "coordinator", "usage limit", until, 0))
		var cards []sprint.CardAdd
		for i := range 4 {
			cards = append(cards, sprint.CardAdd{ID: fmt.Sprintf("f1-%d", i+1), Brief: friendsBrief("friend")})
		}
		r.must(store.AddStep(sprint.AddReq{Stream: "f1", Cards: cards}))
		r.tick()
		r.tick()
		assert.Equal(t, sprint.Held, r.friendStatus("amy"))
		assert.Equal(t, 2, r.friendWidth("amy"))
		beforeAmy := r.friendCards("amy")
		beforeBob := r.friendCards("bob")
		assert.Zero(t, beforeAmy, "a held friend is dealt nothing")
		assert.Equal(t, 4, beforeBob, "bob holds the queue at his room")
		r.tickAt(until)
		assert.Equal(t, sprint.Up, r.friendStatus("amy"))
		assert.Equal(t, 2, r.friendWidth("amy"), "brought up at her nova-config width")
		amy, bob := r.friendCards("amy"), r.friendCards("bob")
		assert.NotZero(t, amy, "the same tick levels cards onto her")
		assert.Less(t, bob, beforeBob, "bob's queue was levelled")
		assert.Equal(t, beforeBob, amy+bob, "the cards moved, none were dropped")
		assert.Equal(t, []string{"amy is back up: usage limit ended"}, r.backNotes())
	})

	t.Run("the note part stays quiet until a return is named", func(t *testing.T) {
		t.Parallel()
		s := &sprint.Snapshot{Now: holdT0, Coordinator: "coordinator"}
		p, due := sprint.TickFriendBack(s, sprint.TickReq{})
		assert.Zero(t, due)
		assert.True(t, p.Empty())
		p, due = sprint.TickFriendBack(s, sprint.TickReq{Back: []sprint.FriendBack{{Name: "amy", Cause: "deaf"}}})
		assert.Zero(t, due)
		require.Len(t, p.Notes, 1)
		assert.Equal(t, sprint.NFriendBack, p.Notes[0].Type)
		assert.Equal(t, "amy is back up: deaf ended", p.Notes[0].What)
		assert.Equal(t, "coordinator", p.Notes[0].To)
		assert.Equal(t, sprint.MachineActor, p.Notes[0].Who)
	})
}

func pass(r *holdRig) {
	r.ctx = sprint.WithFriendProbe(r.ctx, func(string) (sprint.FriendProbe, bool) {
		return sprint.FriendProbe{Session: true}, true
	})
}

func (r *holdRig) at(t time.Time) {
	r.mu.Lock()
	r.now = t
	r.mu.Unlock()
}

func (r *holdRig) tickAt(t time.Time) {
	r.t.Helper()
	r.at(t)
	r.beat()
	_, err := r.st.Tick(r.ctx)
	require.NoError(r.t, err)
}

// tickNoBeat runs a tick without writing a beat over the one under test.
func (r *holdRig) tickNoBeat(t time.Time) {
	r.t.Helper()
	r.at(t)
	_, err := r.st.Tick(r.ctx)
	require.NoError(r.t, err)
}

func (r *holdRig) friendWidth(name string) int {
	r.t.Helper()
	for _, f := range r.rows() {
		if f.Name == name {
			return f.Width
		}
	}
	r.t.Fatalf("no row for %s", name)
	return 0
}

func (r *holdRig) rows() []store.FriendRow {
	r.t.Helper()
	rows, err := r.st.FriendRows(r.ctx, r.st.Now())
	require.NoError(r.t, err)
	return rows
}

func (r *holdRig) friendCards(name string) int {
	r.t.Helper()
	s := r.snap()
	return len(onRow(s, sprint.FriendRow(name), "f1", sprint.Ready, sprint.Working))
}

func (r *holdRig) backNotes() []string {
	r.t.Helper()
	lines, err := r.st.Log(r.ctx)
	require.NoError(r.t, err)
	var out []string
	for _, l := range lines {
		if l.Note != nil && l.Note.Type == sprint.NFriendBack {
			assert.Equal(r.t, sprint.Happened, l.Note.Kind)
			assert.Equal(r.t, sprint.MachineActor, l.Note.Who)
			assert.Equal(r.t, "coordinator", l.Note.To)
			out = append(out, l.Note.What)
		}
	}
	return out
}
