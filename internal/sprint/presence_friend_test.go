package sprint_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend is up on two facts (docs/SPEC-SPRINT.md, friend presence): her daemon
// beats (her last beat at most sprint.FriendBeatLive old), and her own session has
// given evidence: a wake ping her session answered, which the coordinator writes
// as friend health --state up, within sprint.FriendPongWindow, or a card of hers
// finished within sprint.FriendFinishWindow. On 2026-10-04 one friend read up for
// four hours and another read working for an hour on beats a loop sent for them
// while their sessions took no turn; a beat, whoever sends it, never makes her up
// alone, and her row says which of the two is missing.

// presenceRig is the twin store (store.Mem) with an injected clock, a
// coordinator holding the seat at its first generation, and the friend amy.
type presenceRig struct {
	t   *testing.T
	st  *store.Store
	ctx context.Context
	mu  sync.Mutex
	now time.Time
}

var presenceT0 = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

func newPresenceRig(t *testing.T) *presenceRig {
	t.Helper()
	m := store.NewMem()
	r := &presenceRig{t: t, ctx: context.Background(), now: presenceT0}
	r.st = &store.Store{B: m, Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator",
		Now:   func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		NewID: func() string { return "1" }, Sleep: func(time.Duration) {}}
	require.NoError(t, r.st.Init(r.ctx))
	require.NoError(t, m.SetCoordinator(r.ctx, "coordinator"))
	_, _, _, err := r.st.SyncFriends(r.ctx, []store.FriendSpec{{Name: "amy", Width: 2}})
	require.NoError(t, err)
	return r
}

// second moves the clock a second and beats amy, as a loop beside her app did.
func (r *presenceRig) second() {
	r.t.Helper()
	r.mu.Lock()
	r.now = r.now.Add(time.Second)
	r.mu.Unlock()
	_, err := r.st.FriendBeat(r.ctx, "amy")
	require.NoError(r.t, err)
}

// beatFor beats amy every second for d.
func (r *presenceRig) beatFor(d time.Duration) {
	for range int(d / time.Second) {
		r.second()
	}
}

// row is amy's row of the friends table now.
func (r *presenceRig) row() store.FriendRow {
	r.t.Helper()
	rows, err := r.st.FriendRows(r.ctx, r.st.Now())
	require.NoError(r.t, err)
	require.Len(r.t, rows, 1)
	return rows[0]
}

// observe is the coordinator's friend health of amy at the clock now.
func (r *presenceRig) observe(state string) {
	r.t.Helper()
	_, _, _, err := r.st.FriendHealth(r.ctx, "amy", "coordinator", sprint.FriendHealth{State: state, Seen: r.st.Now(), Generation: sprint.FirstSeatGeneration}, "")
	require.NoError(r.t, err)
}

func TestFriendIsUpOnlyOnEvidenceFromHerSession(t *testing.T) {
	t.Parallel()
	r := newPresenceRig(t)

	// beaten every second, no pong, no card: down from the first beat to past the window
	r.second()
	row := r.row()
	assert.Equal(t, sprint.Down, row.Status, "a beat alone is no evidence")
	assert.Contains(t, row.Evidence, "daemon up, session deaf (never heard)", "the row says the daemon beats and the session is the half missing")
	r.beatFor(sprint.FriendPongWindow + time.Minute)
	row = r.row()
	assert.Equal(t, sprint.Down, row.Status, "beaten every second for eleven minutes, still down")
	assert.Contains(t, row.Evidence, "no wake ping answered by her session within 10m0s")
	assert.Contains(t, row.Evidence, "no card finished within 30m0s")

	// her daemon's own pong is no evidence either
	r.observe(sprint.DaemonPong)
	assert.Equal(t, sprint.Down, r.row().Status, "a pong the daemon answered is not her session")

	// one wake ping her session answered: up, naming it and its age
	r.second()
	r.observe(sprint.Up)
	row = r.row()
	assert.Equal(t, sprint.Up, row.Status)
	assert.Equal(t, "daemon up, session pong 0s ago", row.Evidence)
	r.beatFor(sprint.FriendPongWindow - time.Second)
	row = r.row()
	assert.Equal(t, sprint.Up, row.Status, "within the window")
	assert.Equal(t, "daemon up, session pong 9m59s ago", row.Evidence)
	r.second()
	row = r.row()
	assert.Equal(t, sprint.Down, row.Status, "the window out with no other pong: down, beats or not")
	assert.Contains(t, row.Evidence, "daemon up, session deaf 10m0s: no wake ping answered by her session within 10m0s")
	r.beatFor(time.Hour)
	assert.Equal(t, sprint.Down, r.row().Status, "an hour of beats changes nothing")

	// a card of hers finished: up for the finish window, then down
	require.NoError(t, r.st.FriendFinished(r.ctx, "amy", r.st.Now()))
	row = r.row()
	assert.Equal(t, sprint.Up, row.Status)
	assert.Equal(t, "daemon up, finish 0s ago", row.Evidence)
	assert.Equal(t, r.st.Now(), row.Finished)
	require.NoError(t, r.st.FriendFinished(r.ctx, "amy", r.st.Now().Add(-time.Hour)), "an older finish")
	assert.Equal(t, r.st.Now(), r.row().Finished, "an older finish moves nothing")
	r.beatFor(sprint.FriendFinishWindow - time.Second)
	assert.Equal(t, sprint.Up, r.row().Status)
	r.second()
	row = r.row()
	assert.Equal(t, sprint.Down, row.Status)
	assert.Contains(t, row.Evidence, "daemon up, session deaf 30m0s:")
	assert.Contains(t, row.Evidence, "no card finished within 30m0s")

	// her session's evidence with no daemon beating: down, the row naming the beat's age
	r.second()
	r.observe(sprint.Up)
	assert.Equal(t, sprint.Up, r.row().Status)
	r.mu.Lock()
	r.now = r.now.Add(sprint.FriendBeatLive)
	r.mu.Unlock()
	assert.Equal(t, sprint.Up, r.row().Status, "a beat FriendBeatLive old is a daemon beating")
	r.mu.Lock()
	r.now = r.now.Add(time.Second)
	r.mu.Unlock()
	row = r.row()
	assert.Equal(t, sprint.Down, row.Status, "no beat for over FriendBeatLive: her daemon is not beating, whatever her session said")
	assert.Equal(t, "daemon not beating (last beat 11s ago), session pong 11s ago", row.Evidence)
	r.second()
	assert.Equal(t, sprint.Up, r.row().Status, "the next beat brings her back")
}

// The rule itself, with no store: held is held whatever the evidence; a pong
// under an old seat generation, or dated after now, is none; a beat alone is
// down, and so is evidence alone; up is a beat at most FriendBeatLive old and
// evidence within its window.
func TestFriendEvidenceRule(t *testing.T) {
	t.Parallel()
	t0 := presenceT0
	pong := sprint.FriendHealth{State: sprint.Up, Seen: t0, Generation: 2}
	beat := func(at time.Time) sprint.Beat { return sprint.Beat{At: at} }
	cases := []struct {
		name string
		f    sprint.FriendPresence
		at   time.Time
		want string
	}{
		{"never anything", sprint.FriendPresence{Generation: 2}, t0, sprint.Down},
		{"a fresh beat", sprint.FriendPresence{Beat: sprint.Beat{At: t0}, Generation: 2}, t0, sprint.Down},
		{"held with a fresh pong", sprint.FriendPresence{Held: true, Beat: beat(t0), Health: pong, Generation: 2}, t0, sprint.Held},
		{"a fresh pong", sprint.FriendPresence{Beat: beat(t0.Add(sprint.FriendPongWindow - time.Second)), Health: pong, Generation: 2}, t0.Add(sprint.FriendPongWindow - time.Second), sprint.Up},
		{"a fresh pong, no beat", sprint.FriendPresence{Health: pong, Generation: 2}, t0, sprint.Down},
		{"a fresh pong, the beat FriendBeatLive old", sprint.FriendPresence{Beat: beat(t0.Add(-sprint.FriendBeatLive)), Health: pong, Generation: 2}, t0, sprint.Up},
		{"a fresh pong, the beat past FriendBeatLive", sprint.FriendPresence{Beat: beat(t0.Add(-sprint.FriendBeatLive - time.Second)), Health: pong, Generation: 2}, t0, sprint.Down},
		{"a pong the window old", sprint.FriendPresence{Beat: beat(t0.Add(sprint.FriendPongWindow)), Health: pong, Generation: 2}, t0.Add(sprint.FriendPongWindow), sprint.Down},
		{"a pong under an old seat", sprint.FriendPresence{Beat: beat(t0), Health: pong, Generation: 3}, t0, sprint.Down},
		{"a pong from the future", sprint.FriendPresence{Beat: beat(t0.Add(-time.Second)), Health: pong, Generation: 2}, t0.Add(-time.Second), sprint.Down},
		{"a daemon pong", sprint.FriendPresence{Beat: beat(t0), Health: sprint.FriendHealth{State: sprint.DaemonPong, Seen: t0, Generation: 2}, Generation: 2}, t0, sprint.Down},
		{"a fresh finish", sprint.FriendPresence{Beat: beat(t0.Add(sprint.FriendFinishWindow - time.Second)), Finished: t0, Generation: 2}, t0.Add(sprint.FriendFinishWindow - time.Second), sprint.Up},
		{"a finish the window old", sprint.FriendPresence{Beat: beat(t0.Add(sprint.FriendFinishWindow)), Finished: t0, Generation: 2}, t0.Add(sprint.FriendFinishWindow), sprint.Down},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, sprint.FriendStatus(c.f, c.at), c.name)
	}
	assert.Equal(t, 10*time.Minute, sprint.FriendPongWindow, "the owner, 2026-10-05: within 10 minutes")
	assert.Equal(t, 30*time.Minute, sprint.FriendFinishWindow, "the owner, 2026-10-05: within 30 minutes")
	assert.Equal(t, 10*time.Second, sprint.FriendBeatLive, "every-friend-daemon-beats-every-second: up needs the daemon beating within 10 s")

	// the row says which of the two is missing
	_, why := sprint.FriendEvidence(sprint.FriendPresence{Beat: beat(t0), Health: pong, Generation: 2}, t0.Add(14*time.Minute))
	assert.Equal(t, "daemon not beating (last beat 14m0s ago), session deaf 14m0s: no wake ping answered by her session within 10m0s, no card finished within 30m0s", why)
	_, why = sprint.FriendEvidence(sprint.FriendPresence{Beat: beat(t0.Add(14 * time.Minute)), Health: pong, Generation: 2}, t0.Add(14*time.Minute))
	assert.True(t, strings.HasPrefix(why, "daemon up, session deaf 14m0s:"), why)
	_, why = sprint.FriendEvidence(sprint.FriendPresence{Health: pong, Generation: 2}, t0)
	assert.Equal(t, "daemon never beat, session pong 0s ago", why)
}
