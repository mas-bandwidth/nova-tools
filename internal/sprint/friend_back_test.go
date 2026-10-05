package sprint_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A friend back up by the machine (docs/SPEC-SPRINT.md section 1, "A friend back up"; the
// owner, 2026-10-05 9:35 AM ET: "Just like you notice that friends are down, you should
// notice they are back up automatically"), on the twin store with a clock the test steps:
// no test here sleeps on the wall clock.

var backT0 = time.Date(2030, 1, 2, 8, 40, 0, 0, time.UTC)

// backRig is a twin with friends amy and bob (row width 2), the coordinator in the seat, and
// a probe the test answers, counting who it was asked of.
type backRig struct {
	t      *testing.T
	st     *store.Store
	ctx    context.Context
	mu     sync.Mutex
	now    time.Time
	pass   map[string]sprint.FriendProbeResult
	probed map[string]int
}

func newBackRig(t *testing.T) *backRig {
	t.Helper()
	r := &backRig{t: t, ctx: context.Background(), now: backT0, pass: map[string]sprint.FriendProbeResult{}, probed: map[string]int{}}
	n := 0
	r.st = &store.Store{B: store.NewMem(), Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator", ByHand: true,
		Now:   func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		NewID: func() string { r.mu.Lock(); defer r.mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {}}
	require.NoError(t, r.st.Init(r.ctx))
	require.NoError(t, r.st.B.(*store.Mem).SetCoordinator(r.ctx, "coordinator"))
	_, _, _, err := r.st.SyncFriends(r.ctx, []store.FriendSpec{{Name: "amy", Width: 2}, {Name: "bob", Width: 2}})
	require.NoError(t, err)
	return r
}

func (r *backRig) step(d time.Duration) {
	r.mu.Lock()
	r.now = r.now.Add(d)
	r.mu.Unlock()
	for _, f := range []string{"amy", "bob"} {
		_, err := r.st.FriendBeat(r.ctx, f)
		require.NoError(r.t, err)
	}
}

func (r *backRig) probe(friend, cause string) sprint.FriendProbeResult {
	r.probed[friend]++
	return r.pass[friend]
}

// back is one pass of the machine's return; who came back.
func (r *backRig) back() []string {
	r.t.Helper()
	back, _, err := r.st.FriendsBack(r.ctx, r.probe)
	require.NoError(r.t, err)
	var out []string
	for _, b := range back {
		out = append(out, b.Friend)
	}
	return out
}

func (r *backRig) row(friend string) store.FriendRow {
	r.t.Helper()
	rows, err := r.st.FriendRows(r.ctx, r.st.Now())
	require.NoError(r.t, err)
	for _, row := range rows {
		if row.Name == friend {
			return row
		}
	}
	r.t.Fatalf("no row %s", friend)
	return store.FriendRow{}
}

// backNotes is every friend back note the log holds, as written.
func (r *backRig) backNotes() []string {
	r.t.Helper()
	lines, err := r.st.Log(r.ctx)
	require.NoError(r.t, err)
	var out []string
	for _, l := range lines {
		if l.Note != nil && l.Note.Type == sprint.NFriendBack {
			assert.Equal(r.t, "coordinator", l.Note.To, "the note is the coordinator's")
			out = append(out, l.Note.What)
		}
	}
	return out
}

func TestAFriendHeldForACauseThatEndedIsBroughtBackUp(t *testing.T) {
	t.Parallel()
	answered := sprint.FriendProbeResult{Answered: true}
	t.Run("friend down --until comes up at that time after a passing probe, at her row's width", func(t *testing.T) {
		t.Parallel()
		r := newBackRig(t)
		require.NoError(t, r.st.SetFriendHeld(r.ctx, "amy", false, "coordinator", "", time.Time{}, 5)) // friend up --width 5
		require.NoError(t, r.st.SetFriendHeld(r.ctx, "amy", true, "coordinator", "a bench", backT0.Add(time.Hour), 0))
		r.pass["amy"] = answered
		r.step(59 * time.Minute)
		assert.Empty(t, r.back(), "before --until")
		assert.Zero(t, r.probed["amy"], "no probe before the known end")
		assert.Equal(t, sprint.Held, r.row("amy").Status)
		r.step(time.Minute)
		assert.Equal(t, []string{"amy"}, r.back(), "at --until, the probe passing")
		row := r.row("amy")
		assert.Equal(t, sprint.Up, row.Status)
		assert.Equal(t, 2, row.Width, "her row's width, not friend up --width's")
		assert.Equal(t, []string{"amy is back up: the hold (a bench) ended"}, r.backNotes())
	})
	t.Run("an out-of-credit hold is kept while the probe fails and released when it passes", func(t *testing.T) {
		t.Parallel()
		r := newBackRig(t)
		require.NoError(t, r.st.SetFriendHeld(r.ctx, "amy", true, "coordinator", "out of credit", time.Time{}, 0))
		r.pass["amy"] = sprint.FriendProbeResult{Answered: true, FundsRefused: true, Why: "her run refused for funds"}
		r.step(time.Second)
		assert.Empty(t, r.back())
		assert.Equal(t, 1, r.probed["amy"], "an ending cause with no known end is probed at once")
		assert.Equal(t, sprint.Held, r.row("amy").Status, "a run refusing for funds keeps her held")
		r.step(30 * time.Second)
		assert.Empty(t, r.back())
		assert.Equal(t, 1, r.probed["amy"], "not again inside the backoff")
		r.step(30 * time.Second)
		assert.Empty(t, r.back())
		assert.Equal(t, 2, r.probed["amy"], "again once the backoff passed")
		r.pass["amy"] = sprint.FriendProbeResult{Answered: false, Why: "no session pong"}
		r.step(sprint.FriendBackWait(2))
		assert.Empty(t, r.back())
		assert.Equal(t, 3, r.probed["amy"], "the backoff doubles")
		assert.Equal(t, sprint.Held, r.row("amy").Status, "a daemon's answer alone keeps her held")
		assert.Empty(t, r.backNotes())
		r.pass["amy"] = answered
		r.step(sprint.FriendBackWait(3))
		assert.Equal(t, []string{"amy"}, r.back())
		assert.Equal(t, sprint.Up, r.row("amy").Status)
		assert.Equal(t, []string{"amy is back up: out of credit ended"}, r.backNotes())
	})
	t.Run("a hand hold with no cause is never released", func(t *testing.T) {
		t.Parallel()
		r := newBackRig(t)
		require.NoError(t, r.st.SetFriendHeld(r.ctx, "amy", true, "coordinator", "", time.Time{}, 0))
		require.NoError(t, r.st.SetFriendHeld(r.ctx, "bob", true, "coordinator", "the owner asked", time.Time{}, 0))
		r.pass["amy"], r.pass["bob"] = answered, answered
		for range 48 {
			r.step(30 * time.Minute)
			assert.Empty(t, r.back())
		}
		assert.Zero(t, r.probed["amy"]+r.probed["bob"], "never probed")
		assert.Equal(t, sprint.Held, r.row("amy").Status)
		assert.Equal(t, sprint.Held, r.row("bob").Status)
		assert.Empty(t, r.backNotes())
	})
	t.Run("a usage limit the daemon observed comes up at its reset after a passing probe", func(t *testing.T) {
		t.Parallel()
		r := newBackRig(t)
		reset := backT0.Add(2 * time.Hour)
		_, status, _, err := r.st.FriendHealth(r.ctx, "bob", "coordinator", sprint.FriendHealth{State: sprint.Down, Seen: backT0, Generation: sprint.FirstSeatGeneration, Reason: "usage limit", Until: reset}, "")
		require.NoError(t, err)
		require.Equal(t, sprint.Down, status)
		r.pass["bob"] = answered
		r.step(time.Hour)
		assert.Empty(t, r.back())
		assert.Zero(t, r.probed["bob"], "not before the reset")
		r.step(time.Hour)
		assert.Equal(t, []string{"bob"}, r.back())
		assert.Equal(t, sprint.Up, r.row("bob").Status)
		assert.Equal(t, []string{"bob is back up: usage limit ended"}, r.backNotes())
	})
	t.Run("one note per return", func(t *testing.T) {
		t.Parallel()
		r := newBackRig(t)
		require.NoError(t, r.st.SetFriendHeld(r.ctx, "amy", true, "coordinator", "usage limit", backT0.Add(time.Minute), 0))
		r.pass["amy"] = answered
		r.step(time.Minute)
		assert.Equal(t, []string{"amy"}, r.back())
		for range 5 {
			r.step(time.Hour)
			assert.Empty(t, r.back(), "up: nothing to bring back")
		}
		assert.Equal(t, 1, r.probed["amy"])
		assert.Len(t, r.backNotes(), 1)
		// held again for the same cause, back again: a second return, a second note
		require.NoError(t, r.st.SetFriendHeld(r.ctx, "amy", true, "coordinator", "usage limit", r.st.Now().Add(time.Minute), 0))
		r.step(time.Minute)
		assert.Equal(t, []string{"amy"}, r.back())
		assert.Len(t, r.backNotes(), 2)
	})
	t.Run("the cause is read from the hold's words and its end", func(t *testing.T) {
		t.Parallel()
		at := backT0.Add(time.Hour)
		for _, c := range []struct {
			reason string
			until  time.Time
			want   string
		}{
			{"out of credit", time.Time{}, sprint.CauseOutOfCredit},
			{"Insufficient AI Credits, will refresh 6:52 PM", at, sprint.CauseOutOfCredit},
			{"usage limit", at, sprint.CauseUsageLimit},
			{"her model allowance ran out", time.Time{}, sprint.CauseUsageLimit},
			{"deaf", time.Time{}, sprint.CauseDeaf},
			{"a bench", at, sprint.CauseUntil},
			{"a bench", time.Time{}, ""},
			{"", time.Time{}, ""},
		} {
			assert.Equal(t, c.want, sprint.FriendBackCause(c.reason, c.until), "%q until %v", c.reason, c.until)
		}
	})
}
