package hook_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nonces is a nonce source that counts: n1, n2, ...
func nonces() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("n%d", n) }
}

// pass is one heartbeat pass over every name by the server named by.
func (r *rig) beat(t *testing.T, by string, next func() string) map[string]hook.Beat {
	t.Helper()
	names, err := r.s.Names(context.Background())
	require.NoError(t, err)
	got, err := r.s.Pass(context.Background(), names, hook.BeatEvery, hook.MissLimit, next, by)
	require.NoError(t, err)
	out := map[string]hook.Beat{}
	for _, b := range got.Beats {
		out[b.Name] = b
	}
	return out
}

func (r *rig) events(t *testing.T) int {
	t.Helper()
	n, err := r.Client.XLen(context.Background(), hook.EventsKey).Result()
	require.NoError(t, err)
	return int(n)
}

// TestAProvingClientStaysHookedAcrossTenIntervals pins the heartbeat's yes
// (tla/HookBeat.tla, Answer): a session that answers every challenge inside
// its interval is challenged once an interval and never misses.
func TestAProvingClientStaysHookedAcrossTenIntervals(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.Hook(t, "ada", hook.Comms)
	next := nonces()
	for i := range 10 {
		b := r.beat(t, "srv", next)["ada"]
		require.Equal(t, hook.BeatChallenged, b.Did, "interval %d", i)
		_, err := r.s.Answer(context.Background(), "ada", b.Nonce, "session")
		require.NoError(t, err, "interval %d", i)
		r.pass(hook.BeatEvery)
		r.Hook(t, "ada", hook.Comms) // its holder's renewal: joins the record at its epoch
	}
	st := r.state(t, "ada")
	assert.Equal(t, hook.StateProven, st.State)
	assert.Zero(t, st.Misses)
	assert.Zero(t, r.events(t))
}

// TestASilentClientIsUnhookedAfterExactlyTwoAnnouncedOnce pins the
// heartbeat's no (tla/HookBeat.tla, Pass and UnhookOnce): two challenges in a
// row left unanswered unhook the name at the next pass, not before, with the
// reason; two servers passing at every step challenge once and unhook once.
func TestASilentClientIsUnhookedAfterExactlyTwoAnnouncedOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.Hook(t, "ada", hook.Comms)
	next := nonces()
	var did [][2]int
	for range 4 {
		a := r.beat(t, "srv-a", next)["ada"]
		b := r.beat(t, "srv-b", next)["ada"]
		did = append(did, [2]int{a.Did, b.Did})
		r.pass(hook.BeatEvery)
	}
	assert.Equal(t, [][2]int{
		{hook.BeatChallenged, hook.BeatLeft}, // the first challenge
		{hook.BeatChallenged, hook.BeatLeft}, // the first miss, a second challenge
		{hook.BeatUnhooked, hook.BeatLeft},   // the second miss: unhooked
		{hook.BeatLeft, hook.BeatLeft},       // unhooked stays unhooked
	}, did)
	st := r.state(t, "ada")
	assert.Equal(t, hook.StateUnhooked, st.State)
	assert.Equal(t, "missed 2 challenges", st.Reason)
	assert.Equal(t, 1, r.events(t), "the unhook is logged once")
	require.NotNil(t, st.Gate(hook.Want{Name: "ada"}))
	assert.Contains(t, st.Gate(hook.Want{Name: "ada"}).Why, "by the heartbeat (missed 2 challenges)")

	r.Hook(t, "ada", hook.Comms)
	st = r.state(t, "ada")
	assert.Equal(t, hook.StateProven, st.State, "hooking in again starts clean")
	assert.Zero(t, st.Misses)
	assert.Empty(t, st.Reason)
	assert.Equal(t, hook.BeatChallenged, r.beat(t, "srv-a", next)["ada"].Did, "and is challenged at once")
}

// TestOneAnswerBetweenMissesResetsTheCount pins "in a row": a miss, an
// answer, a miss is never an unhook.
func TestOneAnswerBetweenMissesResetsTheCount(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.Hook(t, "ada", hook.Comms)
	next := nonces()
	r.beat(t, "srv", next)
	r.pass(hook.BeatEvery)
	b := r.beat(t, "srv", next)["ada"]
	require.Equal(t, 1, b.Misses)
	_, err := r.s.Answer(context.Background(), "ada", b.Nonce, "session")
	require.NoError(t, err)
	r.pass(hook.BeatEvery)
	r.beat(t, "srv", next)
	r.pass(hook.BeatEvery)
	b = r.beat(t, "srv", next)["ada"]
	assert.Equal(t, hook.BeatChallenged, b.Did)
	assert.Equal(t, 1, b.Misses)
}

// TestAStaleOrWrongNonceIsRefused pins prove's refusals (tla/HookBeat.tla,
// Answer): another nonce, the last interval's nonce, a challenge past its
// interval, and one answered already.
func TestAStaleOrWrongNonceIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	r.Hook(t, "ada", hook.Comms)
	next := nonces()
	old := r.beat(t, "srv", next)["ada"].Nonce
	r.pass(hook.BeatEvery)
	cur := r.beat(t, "srv", next)["ada"].Nonce
	rows := []struct {
		name  string
		nonce string
		pass  time.Duration
		want  error
	}{
		{"another nonce", "nope", 0, hook.ErrWrongNonce},
		{"the last interval's nonce", old, 0, hook.ErrWrongNonce},
		{"the current nonce past its interval", cur, hook.BeatEvery + time.Second, hook.ErrExpired},
	}
	for _, row := range rows {
		r.pass(row.pass)
		_, err := r.s.Answer(ctx, "ada", row.nonce, "session")
		assert.ErrorIs(t, err, row.want, row.name)
	}
	r.Hook(t, "bob", hook.Comms)
	fresh := r.beat(t, "srv", next)["bob"].Nonce
	_, err := r.s.Answer(ctx, "bob", fresh, "session")
	require.NoError(t, err)
	_, err = r.s.Answer(ctx, "bob", fresh, "session")
	assert.ErrorIs(t, err, hook.ErrNoChallenge, "an answered challenge is spent")
}

// TestARoleWithNoBusPushIsNeverChallenged pins who the heartbeat asks: a
// machine's loop has no session to answer.
func TestARoleWithNoBusPushIsNeverChallenged(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.Hook(t, "m1", hook.Member)
	next := nonces()
	for range 4 {
		assert.Equal(t, hook.BeatLeft, r.beat(t, "srv", next)["m1"].Did)
		r.pass(hook.BeatEvery)
	}
	assert.NotEqual(t, hook.StateUnhooked, r.state(t, "m1").State)
}

// TestTheServerStampsItsOwnRow pins Serve: no stamp before a pass; after it,
// the interval and the store's time.
func TestTheServerStampsItsOwnRow(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	sv, err := r.s.Serve(context.Background())
	require.NoError(t, err)
	assert.False(t, sv.OK)
	r.beat(t, "srv", nonces())
	r.pass(time.Second)
	sv, err = r.s.Serve(context.Background())
	require.NoError(t, err)
	assert.True(t, sv.OK)
	assert.Equal(t, hook.BeatEvery, sv.Every)
	assert.Equal(t, time.Second, sv.Now.Sub(sv.At))
}

// passAll is one pass over every name by the server named by, as it came.
func (r *rig) passAll(t *testing.T, by string, next func() string) hook.Passed {
	t.Helper()
	names, err := r.s.Names(context.Background())
	require.NoError(t, err)
	got, err := r.s.Pass(context.Background(), names, hook.BeatEvery, hook.MissLimit, next, by)
	require.NoError(t, err)
	return got
}

// TestEachChangeIsToldOncePerTransition pins what a pass tells
// (tla/HookBeat.tla, Tell): a name down, a name back and the coordinator
// changing are each told by exactly one pass, never again for the same
// state, and a second server's pass in the same interval tells nothing.
func TestEachChangeIsToldOncePerTransition(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.Hook(t, "ada", hook.Coordinator)
	r.Hook(t, "bob", hook.Comms)
	next := nonces()
	type told struct {
		down, back []string
		coord, was string
	}
	var got []told
	step := func() {
		p := r.passAll(t, "srv-a", next)
		require.True(t, p.Taken)
		assert.False(t, r.passAll(t, "srv-b", next).Taken, "a second server inside the interval")
		got = append(got, told{p.Down, p.Back, p.Coord, p.WasCoord})
		r.pass(hook.BeatEvery)
	}
	step()
	step()
	step() // both missed twice: unhooked, the coordinator with them
	step()
	r.Hook(t, "ada", hook.Coordinator)
	step() // ada back, and the coordinator again
	step()
	assert.Equal(t, []told{
		{nil, nil, "ada", ""}, {nil, nil, "ada", "ada"},
		{[]string{"ada", "bob"}, nil, "", "ada"}, {nil, nil, "", ""},
		{nil, []string{"ada"}, "ada", ""}, {nil, nil, "ada", "ada"},
	}, got)
}

// TestAFriendsAnswerProvesItsWorkPush pins Challenged: a friend's challenge
// names its work push as well as its bus push, so the session's answer
// proves both, with no daemon's proof.
func TestAFriendsAnswerProvesItsWorkPush(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	r.Hook(t, "ada", hook.Friend)
	st := r.state(t, "ada")
	for _, p := range []hook.Push{hook.Bus, hook.Work} {
		require.NoError(t, r.s.Unprove(ctx, "ada", hook.Friend, p, st.Epoch))
	}
	require.Equal(t, hook.StateLive, r.state(t, "ada").State)
	b := r.beat(t, "srv", nonces())["ada"]
	n, err := r.s.Answer(ctx, "ada", b.Nonce, "session")
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, hook.StateProven, r.state(t, "ada").State)
}
