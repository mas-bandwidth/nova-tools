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
// heartbeat's no (tla/HookBeat.tla, Pass and UnhookOnce): MissLimit challenges
// in a row left unanswered unhook the name at the next pass, not before, with
// the reason; two servers passing at every step challenge once and unhook once.
func TestASilentClientIsUnhookedAfterExactlyTwoAnnouncedOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.Hook(t, "ada", hook.Comms)
	next := nonces()
	var did [][2]int
	for range hook.MissLimit + 2 {
		a := r.beat(t, "srv-a", next)["ada"]
		b := r.beat(t, "srv-b", next)["ada"]
		did = append(did, [2]int{a.Did, b.Did})
		r.pass(hook.BeatEvery)
	}
	assert.Equal(t, [][2]int{
		{hook.BeatChallenged, hook.BeatLeft}, // the first challenge
		{hook.BeatChallenged, hook.BeatLeft}, // the first miss, a second challenge
		{hook.BeatChallenged, hook.BeatLeft}, // the second miss
		{hook.BeatChallenged, hook.BeatLeft}, // the third miss
		{hook.BeatChallenged, hook.BeatLeft}, // the fourth miss
		{hook.BeatUnhooked, hook.BeatLeft},   // the fifth miss (5m): unhooked
		{hook.BeatLeft, hook.BeatLeft},       // unhooked stays unhooked
	}, did)
	st := r.state(t, "ada")
	assert.Equal(t, hook.StateUnhooked, st.State)
	assert.Equal(t, "missed 5 challenges", st.Reason)
	assert.Equal(t, 1, r.events(t), "the unhook is logged once")
	require.NotNil(t, st.Gate(hook.Want{Name: "ada"}))
	assert.Contains(t, st.Gate(hook.Want{Name: "ada"}).Why, "by the heartbeat (missed 5 challenges)")

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
// the interval, the down-after window and the store's time.
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
	assert.Equal(t, hook.DownAfter, sv.DownAfter)
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

// TestAChangeNotMarkedToldIsFoundAgain pins mark-after-delivery at the
// store: until the server marks an audience told, every pass finds the same
// change for it; once marked, none.
func TestAChangeNotMarkedToldIsFoundAgain(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.Hook(t, "ada", hook.Comms)
	next := nonces()
	for range hook.MissLimit + 1 {
		r.passAll(t, "srv", next)
		r.pass(hook.BeatEvery)
	}
	for range 2 {
		p := r.passAll(t, "srv", next)
		down, _, _ := p.Diff(hook.ToCoordinator)
		assert.Equal(t, []string{"ada"}, down, "not yet marked: found again")
		r.pass(hook.BeatEvery)
	}
	p := r.passAll(t, "srv", next)
	require.NoError(t, r.s.Told(context.Background(), p, hook.ToCoordinator))
	r.pass(hook.BeatEvery)
	p = r.passAll(t, "srv", next)
	assert.False(t, p.Changed(hook.ToCoordinator), "marked: told once")
	assert.True(t, p.Changed(hook.ToRest), "each audience is marked on its own")
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
		down, back, was := p.Diff(hook.ToRest)
		got = append(got, told{down, back, p.Coord, was})
		for _, a := range hook.Audiences { // delivered: marked told
			require.NoError(t, r.s.Told(context.Background(), p, a))
		}
		r.pass(hook.BeatEvery)
	}
	for range hook.MissLimit {
		step()
	}
	step() // both missed MissLimit challenges: unhooked, the coordinator with them
	step()
	r.Hook(t, "ada", hook.Coordinator)
	step() // ada back, and the coordinator again
	step()
	assert.Equal(t, []told{
		{nil, nil, "ada", ""},
		{nil, nil, "ada", "ada"}, {nil, nil, "ada", "ada"}, {nil, nil, "ada", "ada"}, {nil, nil, "ada", "ada"},
		{[]string{"ada", "bob"}, nil, "", "ada"}, {nil, nil, "", ""},
		{nil, []string{"ada"}, "ada", ""}, {nil, nil, "ada", "ada"},
	}, got)
}

// TestAFriendsAnswerProvesItsWorkPush pins that a friend-role name needs only
// its bus push and an answered challenge to read proven (up), with no daemon
// work push required.
func TestAFriendsAnswerProvesItsWorkPush(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	e, _, err := r.s.HookAs(ctx, "ada", hook.Friend, "session")
	require.NoError(t, err)
	require.NoError(t, r.s.Renew(ctx, "ada", hook.Friend, hook.Bus, e, "session"))
	require.Equal(t, hook.StateLive, r.state(t, "ada").State)
	b := r.beat(t, "srv", nonces())["ada"]
	n, err := r.s.Answer(ctx, "ada", b.Nonce, "session")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	st := r.state(t, "ada")
	assert.Equal(t, hook.StateProven, st.State)
	assert.Empty(t, st.Down)
	assert.NoError(t, r.s.Gate(ctx, hook.Want{Name: "ada", Need: []hook.Push{hook.Bus}}))
}

// TestTheGateRefusesEveryoneWhileTheServerIsStale pins the one staleness
// rule in the shared gate (Served.Stale, read at the check by the store's
// TIME): a hooked, proven record passes while the server row is fresh and is
// refused, "hook server stale", once it is older than two intervals.
func TestTheGateRefusesEveryoneWhileTheServerIsStale(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	r.Hook(t, "ada", hook.Comms)
	want := hook.Want{Name: "ada", Need: []hook.Push{hook.Bus}}
	b := r.beat(t, "srv", nonces())["ada"]
	_, err := r.s.Answer(ctx, "ada", b.Nonce, "session")
	require.NoError(t, err)
	r.pass(2 * hook.BeatEvery)
	r.Hook(t, "ada", hook.Comms) // its holder's renewal: the record itself is fresh
	require.NoError(t, r.s.Gate(ctx, want), "two intervals exactly: fresh")
	r.pass(time.Second)
	r.Hook(t, "ada", hook.Comms)
	err = r.s.Gate(ctx, want)
	var refusal *hook.Refusal
	require.ErrorAs(t, err, &refusal)
	assert.Contains(t, refusal.Why, "hook server stale")
	b = r.beat(t, "srv", nonces())["ada"]
	_, err = r.s.Answer(ctx, "ada", b.Nonce, "session")
	require.NoError(t, err)
	assert.NoError(t, r.s.Gate(ctx, want), "the server passing again, and answered: up")
}

// TestNoServerOrNoAnswerIsNoEvidence pins "up only with evidence": a hooked,
// proven record with no server row at all is refused, "hook server never
// ran"; with a server that challenged it and no answer yet, refused; once it
// answered, it passes.
func TestNoServerOrNoAnswerIsNoEvidence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	r.Hook(t, "ada", hook.Comms)
	require.NoError(t, r.Client.Del(ctx, hook.ServeKey).Err()) // the rig's evidence taken away: no server ever ran
	require.NoError(t, r.Client.HDel(ctx, hook.RecordKey("ada"), "answered_at").Err())
	want := hook.Want{Name: "ada", Need: []hook.Push{hook.Bus}}
	var refusal *hook.Refusal
	require.ErrorAs(t, r.s.Gate(ctx, want), &refusal)
	assert.Contains(t, refusal.Why, "hook server never ran")
	b := r.beat(t, "srv", nonces())["ada"]
	require.ErrorAs(t, r.s.Gate(ctx, want), &refusal)
	assert.Contains(t, refusal.Why, "no answer to a heartbeat challenge")
	_, err := r.s.Answer(ctx, "ada", b.Nonce, "session")
	require.NoError(t, err)
	assert.NoError(t, r.s.Gate(ctx, want))
}

// TestAStaleAnswerIsDownWithAFreshServer pins the answer's own window
// (tla/HookBeat.tla; the model's BadUpAfterOutage): with the server passing
// every interval, a name whose last answer is older than the server's
// down-after window by the store's TIME reads down, though it is still hooked.
func TestAStaleAnswerIsDownWithAFreshServer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	r.Hook(t, "ada", hook.Comms)
	want := hook.Want{Name: "ada", Need: []hook.Push{hook.Bus}}
	next := nonces()
	b := r.beat(t, "srv", next)["ada"]
	_, err := r.s.Answer(ctx, "ada", b.Nonce, "session")
	require.NoError(t, err)
	r.pass(hook.BeatEvery)
	r.beat(t, "srv", next) // answered nothing since
	r.pass(hook.BeatEvery)
	r.beat(t, "srv", next)
	r.Hook(t, "ada", hook.Comms) // its holder's renewal only
	require.NoError(t, r.Client.HSet(ctx, hook.RecordKey("ada"), "answered_at", r.state(t, "ada").BeatAt.Add(-hook.DownAfter).UnixMilli()).Err())
	require.NoError(t, r.s.Gate(ctx, want), "down-after window (5m) exactly: up")
	r.pass(time.Second)
	var refusal *hook.Refusal
	require.ErrorAs(t, r.s.Gate(ctx, want), &refusal)
	assert.Contains(t, refusal.Why, hook.NoAnswerReason)
	assert.Equal(t, hook.StateProven, r.state(t, "ada").State, "hooked and proven all the same: only the evidence is old")
}

// TestDownAfterIsSeparateFromEvery pins that --down-after (5m) is separate from
// --every (1m or 2m30s), and that restarting a server with a wider --every
// inside the previous pass's window still updates hook:serve's every and
// down_after so readers judge server staleness from the new --every instead of
// showing everyone down.
func TestDownAfterIsSeparateFromEvery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	r.Hook(t, "ada", hook.Comms)
	want := hook.Want{Name: "ada", Need: []hook.Push{hook.Bus}}
	next := nonces()

	// Start with 1m every, 5m down-after: answer once, then miss 4 challenges (4m) -> still up!
	p, err := r.s.PassWindow(ctx, []string{"ada"}, time.Minute, 5*time.Minute, hook.MissesFor(time.Minute, 5*time.Minute), next, "srv")
	require.NoError(t, err)
	_, err = r.s.Answer(ctx, "ada", p.Beats[0].Nonce, "session")
	require.NoError(t, err)
	ansAt := r.state(t, "ada").AnswerAt
	for range 4 {
		r.pass(time.Minute)
		r.Hook(t, "ada", hook.Comms) // keep lease fresh, restore original answer_at
		require.NoError(t, r.Client.HSet(ctx, hook.RecordKey("ada"), "answered_at", ansAt.UnixMilli()).Err())
		p, err = r.s.PassWindow(ctx, []string{"ada"}, time.Minute, 5*time.Minute, hook.MissesFor(time.Minute, 5*time.Minute), next, "srv")
		require.NoError(t, err)
		require.Equal(t, hook.BeatChallenged, p.Beats[0].Did)
		require.NoError(t, r.s.Gate(ctx, want), "missed <5m with 1m challenges: still up")
	}

	// Now restart server with --every 2m30s, --down-after 5m right after a pass (10s later):
	// the pass is skipped (10s < 0.9 * 2m30s), yet hook:serve updates every=2m30s so
	// readers do not judge the server stale after 2m while it sleeps for 2m30s.
	_, err = r.s.Answer(ctx, "ada", p.Beats[0].Nonce, "session")
	require.NoError(t, err)
	ansAt = r.state(t, "ada").AnswerAt
	r.pass(10 * time.Second)
	every230 := 2*time.Minute + 30*time.Second
	p, err = r.s.PassWindow(ctx, []string{"ada"}, every230, 5*time.Minute, hook.MissesFor(every230, 5*time.Minute), next, "srv")
	require.NoError(t, err)
	assert.False(t, p.Taken, "inside 9/10 of 2m30s: pass skipped")
	sv, err := r.s.Serve(ctx)
	require.NoError(t, err)
	assert.Equal(t, every230, sv.Every, "hook:serve every updated even when pass skipped")
	assert.Equal(t, 5*time.Minute, sv.DownAfter)

	// Advance 2m20s more (total 2m30s since last pass): server is NOT stale because every is 2m30s (stale > 5m).
	r.pass(2*time.Minute + 20*time.Second)
	r.Hook(t, "ada", hook.Comms)
	require.NoError(t, r.Client.HSet(ctx, hook.RecordKey("ada"), "answered_at", ansAt.UnixMilli()).Err())
	require.NoError(t, r.s.Gate(ctx, want), "at 2m30s with --every 2m30s: server is fresh and friend is up")
}
