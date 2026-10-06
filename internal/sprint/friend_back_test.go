package sprint_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend back up (docs/SPEC-SPRINT.md section 1, "A friend back up"; the owner,
// 2026-10-05 ~9:35 AM ET: "Just like you notice that friends are down, you should notice
// they are back up automatically."): a friend held or down for a cause that ends is
// probed by the tick on a backoff and at the known end, and brought up at her row's
// width when her session's evidence passes the probe, levelled the same tick, with one
// note to the coordinator; a hold by hand is never released by the machine.

// backRig is a running sprint on the twin with friends amy and bob (width 2) and no
// fleet member: stream f1 of friends' cards waits for a friend up. The clock moves only
// when the test moves it; wakes records each wake the tick sent.
type backRig struct {
	t     *testing.T
	st    *store.Store
	ctx   context.Context
	mu    sync.Mutex
	now   time.Time
	wakes []string
}

var backT0 = time.Date(2030, 2, 3, 4, 5, 6, 0, time.UTC)

func newBackRig(t *testing.T) *backRig {
	t.Helper()
	m := store.NewMem()
	r := &backRig{t: t, ctx: context.Background(), now: backT0}
	n := 0
	r.st = &store.Store{B: m, Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator",
		Now:   func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		NewID: func() string { r.mu.Lock(); defer r.mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {},
		WakeFriend: func(f string, rung int, _ time.Duration) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.wakes = append(r.wakes, fmt.Sprintf("%s rung=%d", f, rung))
			return nil
		}}
	require.NoError(t, r.st.Init(r.ctx))
	require.NoError(t, m.SetCoordinator(r.ctx, "coordinator"))
	_, _, _, err := r.st.SyncFriends(r.ctx, []store.FriendSpec{{Name: "amy", Width: 2, Class: "pro"}, {Name: "bob", Width: 2, Class: "pro"}})
	require.NoError(t, err)
	var cards []sprint.CardAdd
	for i := range 6 {
		cards = append(cards, sprint.CardAdd{ID: fmt.Sprintf("f1-%d", i+1), Brief: friendsBrief("friend")})
	}
	res, err := r.st.Run(r.ctx, store.AddStep(sprint.AddReq{Stream: "f1", Cards: cards}))
	require.NoError(t, err)
	require.Empty(t, res.Refused)
	_, _, _, err = r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	return r
}

// at sets the clock to d after backT0.
func (r *backRig) at(d time.Duration) {
	r.mu.Lock()
	r.now = backT0.Add(d)
	r.mu.Unlock()
}

// pong is the coordinator's observation that the friend's session answered a wake ping now.
func (r *backRig) pong(f string) {
	r.t.Helper()
	_, _, _, err := r.st.FriendHealth(r.ctx, f, "coordinator", sprint.FriendHealth{State: sprint.Up, Seen: r.st.Now(), Generation: sprint.FirstSeatGeneration}, "")
	require.NoError(r.t, err)
}

// beatDown is her daemon's word that she is down until then, and why; beatUp withdraws it.
func (r *backRig) beatDown(f string, until time.Time, why string) {
	r.t.Helper()
	zero := 0
	_, err := r.st.FriendBeatReport(r.ctx, f, sprint.FriendReport{Working: &zero, Until: until, Reason: why}, nil)
	require.NoError(r.t, err)
}

func (r *backRig) beatUp(f string) {
	r.t.Helper()
	_, err := r.st.FriendBeat(r.ctx, f)
	require.NoError(r.t, err)
}

func (r *backRig) tick() {
	r.t.Helper()
	_, err := r.st.Tick(r.ctx)
	require.NoError(r.t, err)
}

func (r *backRig) status(f string) string {
	r.t.Helper()
	rows, err := r.st.FriendRows(r.ctx, r.st.Now())
	require.NoError(r.t, err)
	for _, row := range rows {
		if row.Name == f {
			return row.Status
		}
	}
	return ""
}

// backNotes is the log's notes of friends back up, as written.
func (r *backRig) backNotes() []string {
	r.t.Helper()
	lines, err := r.st.Log(r.ctx)
	require.NoError(r.t, err)
	var out []string
	for _, l := range lines {
		if l.Note != nil && l.Note.Type == sprint.NFriendBack {
			assert.Equal(r.t, "coordinator", l.Note.To, "the note goes to the coordinator")
			out = append(out, l.Note.What)
		}
	}
	return out
}

func (r *backRig) woken() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.wakes...)
}

// dealt is the friend's cards ready or working on her row.
func (r *backRig) dealt(f string) int {
	r.t.Helper()
	s, err := r.st.Load(r.ctx, store.All, nil)
	require.NoError(r.t, err)
	row := sprint.FriendRow(f)
	return len(s.Fleet.Cell(row, sprint.Ready)) + len(s.Fleet.Cell(row, sprint.Working))
}

func TestAFriendHeldForACauseThatEndedIsBroughtBackUp(t *testing.T) {
	t.Parallel()
	t.Run("a hold until a time comes up at that time after a passing probe, levelled the same tick", func(t *testing.T) {
		t.Parallel()
		r := newBackRig(t)
		until := backT0.Add(time.Hour)
		require.NoError(t, r.st.SetFriendHeld(r.ctx, "amy", true, "coordinator", "on leave", until, 0))
		require.NoError(t, r.st.SetFriendHeld(r.ctx, "bob", true, "coordinator", "", time.Time{}, 0))
		r.at(10 * time.Minute)
		r.pong("amy")
		r.tick()
		assert.Equal(t, sprint.Held, r.status("amy"), "before her end a pong does not release her")
		assert.Empty(t, r.woken(), "no wake before the known end")
		r.at(time.Hour)
		r.tick()
		assert.Equal(t, []string{"amy rung=0"}, r.woken(), "a wake at the known end")
		assert.Equal(t, sprint.Held, r.status("amy"), "no evidence after the end yet")
		assert.Zero(t, r.dealt("amy"))
		r.at(time.Hour + 30*time.Second)
		r.pong("amy")
		r.tick()
		assert.Equal(t, sprint.Up, r.status("amy"), "her session answered after the end")
		assert.Equal(t, 4, r.dealt("amy"), "her queue filled to twice her row's width the same tick")
		r.at(time.Hour + time.Minute)
		r.pong("amy")
		r.tick()
		assert.Equal(t, []string{"amy is back up: on leave ended"}, r.backNotes(), "one note per return")
	})
	t.Run("an out-of-credit hold is released when the probe passes and kept when it fails", func(t *testing.T) {
		t.Parallel()
		r := newBackRig(t)
		require.NoError(t, r.st.SetFriendHeld(r.ctx, "amy", true, "coordinator", "out of credit", time.Time{}, 0))
		r.at(time.Minute)
		r.tick()
		assert.Empty(t, r.woken(), "the first wake waits its first backoff")
		r.at(sprint.FriendBackFirst)
		r.tick()
		assert.Equal(t, []string{"amy rung=0"}, r.woken())
		r.at(sprint.FriendBackFirst + time.Minute)
		r.tick()
		assert.Len(t, r.woken(), 1, "the next wake waits double")
		// her session answers and her daemon says her runs still refuse for funds
		r.at(sprint.FriendBackFirst + 2*time.Minute)
		r.beatDown("amy", backT0.Add(24*time.Hour), "out of credits")
		r.pong("amy")
		r.tick()
		assert.Equal(t, sprint.Held, r.status("amy"), "a run refusing for funds fails the probe")
		r.at(3 * sprint.FriendBackFirst)
		r.tick()
		assert.Len(t, r.woken(), 2, "the second wake, double the first wait after it")
		r.at(3*sprint.FriendBackFirst + time.Minute)
		r.beatUp("amy")
		r.pong("amy")
		r.tick()
		assert.Equal(t, sprint.Up, r.status("amy"))
		assert.Equal(t, []string{"amy is back up: out of credit ended"}, r.backNotes())
	})
	t.Run("a hold by hand with no cause is never released", func(t *testing.T) {
		t.Parallel()
		r := newBackRig(t)
		require.NoError(t, r.st.SetFriendHeld(r.ctx, "bob", true, "coordinator", "stalled", time.Time{}, 0))
		for i := 1; i <= 30; i++ {
			r.at(time.Duration(i) * 5 * time.Minute)
			r.pong("bob")
			r.tick()
		}
		assert.Equal(t, sprint.Held, r.status("bob"))
		assert.Empty(t, r.woken())
		assert.Empty(t, r.backNotes())
		assert.Zero(t, r.dealt("bob"))
	})
	t.Run("a usage limit her beat says comes up after its reset on her session's answer, told once", func(t *testing.T) {
		t.Parallel()
		r := newBackRig(t)
		reset := backT0.Add(2 * time.Hour)
		r.beatDown("bob", reset, "usage limit")
		r.at(2 * time.Hour)
		r.tick()
		assert.Equal(t, sprint.Down, r.status("bob"), "her beat's word stands with no answer after the reset")
		assert.Equal(t, []string{"bob rung=0"}, r.woken(), "a wake at the reset")
		r.at(2*time.Hour + 20*time.Second)
		r.pong("bob")
		r.tick()
		assert.Equal(t, sprint.Up, r.status("bob"))
		assert.Equal(t, 4, r.dealt("bob"))
		for i := 1; i <= 3; i++ {
			r.at(2*time.Hour + time.Duration(i)*time.Minute)
			r.pong("bob")
			r.tick()
		}
		assert.Equal(t, []string{"bob is back up: usage limit ended"}, r.backNotes())
	})
}
