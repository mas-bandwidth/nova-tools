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

// testDownAfter is the down-after of these tests' passes: two intervals, so
// a silent name goes down at the third pass, as the store's tests have always
// read; the default (DownAfter) is pinned by TestDownAfterIsTheReadersWindow
// and cmd/nova-hook's serve tests.
const testDownAfter = 90 * time.Second

// pass is one heartbeat pass over every name by the server named by.
func (r *rig) beat(t *testing.T, by string, next func() string) map[string]hook.Beat {
	t.Helper()
	return r.beatAfter(t, by, next, testDownAfter)
}

// beatAfter is beat with the pass's down-after named.
func (r *rig) beatAfter(t *testing.T, by string, next func() string, downAfter time.Duration) map[string]hook.Beat {
	t.Helper()
	names, err := r.s.Names(context.Background())
	require.NoError(t, err)
	got, err := r.s.Pass(context.Background(), names, hook.BeatEvery, downAfter, next, by)
	require.NoError(t, err)
	out := map[string]hook.Beat{}
	for _, b := range got.Beats {
		if b.Did == hook.BeatChallenged {
			require.NoError(t, r.s.Delivered(context.Background(), b))
		}
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
	r.startHeartbeat(t)
	next := nonces()
	for range 10 {
		r.pass(hook.QuietAfter + time.Minute)
		b := r.beat(t, "srv", next)["ada"]
		require.Equal(t, hook.BeatChallenged, b.Did)
		require.NoError(t, r.s.Renew(context.Background(), "ada", hook.Comms, hook.Bus, b.Epoch, "holder"))
		_, err := r.s.Answer(context.Background(), "ada", b.Nonce, "session")
		require.NoError(t, err)
	}
	assert.Equal(t, hook.StateProven, r.state(t, "ada").State)
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
	r.startHeartbeat(t)
	next := nonces()
	r.pass(hook.QuietAfter + time.Minute)
	assert.Equal(t, hook.BeatChallenged, r.beat(t, "srv-a", next)["ada"].Did)
	assert.Equal(t, hook.BeatLeft, r.beat(t, "srv-b", next)["ada"].Did)
	r.pass(hook.AnswerBound)
	assert.Equal(t, hook.BeatLeft, r.beat(t, "srv-a", next)["ada"].Did)
	r.pass(time.Minute)
	assert.Equal(t, hook.BeatUnhooked, r.beat(t, "srv-a", next)["ada"].Did)
	assert.Equal(t, hook.BeatLeft, r.beat(t, "srv-b", next)["ada"].Did)
	assert.Equal(t, "no answer in 1m30s (1 challenges missed)", r.state(t, "ada").Reason)
	assert.Equal(t, 1, r.events(t))
	require.NotNil(t, r.state(t, "ada").Gate(hook.Want{Name: "ada"}))
	r.Hook(t, "ada", hook.Comms)
	r.startHeartbeat(t)
	assert.Equal(t, hook.StateProven, r.state(t, "ada").State)
	r.pass(time.Minute)
	assert.Equal(t, hook.BeatLeft, r.beat(t, "srv-a", next)["ada"].Did)
}

// TestOneAnswerResetsTheSilence pins that the silence runs from the last
// answer (tla/HookBeat.tla, NoDownBeforeDownAfter): under the default
// down-after, four misses, an answer, four misses is never an unhook, the
// answer resets the count of misses the reason carries, and the first pass
// more than five minutes after the answer unhooks.
func TestOneAnswerResetsTheSilence(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.Hook(t, "ada", hook.Comms)
	r.startHeartbeat(t)
	next := nonces()
	ctx := context.Background()
	r.pass(hook.QuietAfter + time.Minute)
	b := r.beatAfter(t, "srv", next, hook.DownAfter)["ada"]
	r.pass(4 * time.Minute)
	require.NoError(t, r.s.Renew(ctx, "ada", hook.Comms, hook.Bus, b.Epoch, "holder"))
	_, err := r.s.Answer(ctx, "ada", b.Nonce, "session")
	require.NoError(t, err)
	assert.Zero(t, r.state(t, "ada").Misses)
	r.pass(hook.QuietAfter)
	assert.Equal(t, hook.BeatLeft, r.beatAfter(t, "srv", next, hook.DownAfter)["ada"].Did)
	r.pass(time.Minute)
	assert.Equal(t, hook.BeatChallenged, r.beatAfter(t, "srv", next, hook.DownAfter)["ada"].Did)
	r.pass(hook.AnswerBound + time.Minute)
	b = r.beatAfter(t, "srv", next, hook.DownAfter)["ada"]
	assert.Equal(t, hook.BeatUnhooked, b.Did)
	assert.Equal(t, 1, b.Misses)
}

// TestAStaleOrWrongNonceIsRefused pins prove's refusals (tla/HookBeat.tla,
// AnswerIssued and ProofNamesIssuedInWindow): another nonce, a nonce older
// than the down-after window (the refusal names it), another name's nonce,
// one of an earlier epoch, the current nonce past its window, and one
// answered already.
func TestAStaleOrWrongNonceIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	r.Hook(t, "ada", hook.Comms)
	r.startHeartbeat(t)
	r.Hook(t, "bob", hook.Comms)
	r.startHeartbeat(t)
	next := nonces()
	r.pass(hook.QuietAfter + time.Minute)
	cur := r.beatAfter(t, "srv", next, hook.DownAfter)
	for _, nonce := range []string{"nope", cur["bob"].Nonce} {
		_, err := r.s.Answer(ctx, "ada", nonce, "session")
		assert.ErrorIs(t, err, hook.ErrWrongNonce)
	}
	r.pass(hook.DownAfter + time.Second)
	_, err := r.s.Answer(ctx, "ada", cur["ada"].Nonce, "session")
	assert.ErrorIs(t, err, hook.ErrExpired)
	_, _, err = r.s.Arm(ctx, "bob", hook.Comms, "test", true)
	require.NoError(t, err)
	r.Hook(t, "bob", hook.Comms)
	r.startHeartbeat(t)
	r.pass(hook.QuietAfter + time.Minute)
	fresh := r.beatAfter(t, "srv", next, hook.DownAfter)["bob"]
	_, err = r.s.Answer(ctx, "bob", cur["bob"].Nonce, "session")
	assert.ErrorIs(t, err, hook.ErrWrongNonce)
	require.NoError(t, r.s.Renew(ctx, "bob", hook.Comms, hook.Bus, fresh.Epoch, "holder"))
	_, err = r.s.Answer(ctx, "bob", fresh.Nonce, "session")
	require.NoError(t, err)
	_, err = r.s.Answer(ctx, "bob", fresh.Nonce, "session")
	assert.ErrorIs(t, err, hook.ErrNoChallenge)
}

// TestALateDeliveredNonceProves pins the proof window (tla/HookBeat.tla,
// AnswerIssued; WitnessProveOlder): a nonce issued an interval ago answers
// after a newer challenge was issued, says which challenge it matched and
// when it was issued, counts as an answer now (misses cleared, answered_at
// now), leaves the newer challenge standing for its own answer, and is
// spent: the same nonce again is refused.
func TestALateDeliveredNonceProves(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	r.Hook(t, "ada", hook.Comms)
	r.startHeartbeat(t)
	next := nonces()
	r.pass(hook.QuietAfter + time.Minute)
	cur := r.beatAfter(t, "srv", next, hook.DownAfter)["ada"]
	_, at, err := r.s.Read(ctx, "ada")
	require.NoError(t, err)
	// An upgrade preserves an earlier server's still-valid recent nonce.
	oldAt := at.Add(-time.Minute)
	recent := r.Client.HGet(ctx, hook.RecordKey("ada"), "recent").Val()
	old := fmt.Sprintf("%d:%s:%d:%d:bus", cur.Epoch, hook.Sum("old"), oldAt.UnixMilli(), oldAt.Add(hook.DownAfter).UnixMilli())
	require.NoError(t, r.Client.HSet(ctx, hook.RecordKey("ada"), "recent", recent+" "+old).Err())
	require.NoError(t, r.s.Renew(ctx, "ada", hook.Comms, hook.Bus, cur.Epoch, "holder"))
	p, err := r.s.AnswerProof(ctx, "ada", "old", "session")
	require.NoError(t, err)
	assert.Equal(t, hook.Proof{Proven: 1, Back: 1, Issued: oldAt}, p)
	_, err = r.s.Answer(ctx, "ada", "old", "session")
	assert.ErrorIs(t, err, hook.ErrWrongNonce)
	_, err = r.s.Answer(ctx, "ada", cur.Nonce, "session")
	require.NoError(t, err)
}

// TestAFriendProvingOneIntervalLateStaysUp pins the bug the window fixes: a
// harness that hands every challenge to the model 50 s late, after the server
// issued the next one, proves each nonce it was handed; under the default
// down-after the name is never unhooked and never reads down. Before the
// window every such proof was refused, and the name went down.
func TestAFriendProvingOneIntervalLateStaysUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	r.Hook(t, "ada", hook.Comms)
	r.startHeartbeat(t)
	next := nonces()
	for range 12 {
		r.pass(hook.QuietAfter + time.Minute)
		b := r.beatAfter(t, "srv", next, hook.DownAfter)["ada"]
		require.Equal(t, hook.BeatChallenged, b.Did)
		r.pass(110 * time.Second)
		require.NoError(t, r.s.Renew(ctx, "ada", hook.Comms, hook.Bus, b.Epoch, "holder"))
		p, err := r.s.AnswerProof(ctx, "ada", b.Nonce, "session")
		require.NoError(t, err)
		assert.Zero(t, p.Back)
		assert.Equal(t, hook.StateProven, r.state(t, "ada").State)
		assert.Empty(t, r.state(t, "ada").Down)
	}
	assert.Zero(t, r.events(t))
}

// TestARoleWithNoBusPushIsNeverChallenged pins who the heartbeat asks: a
// machine's loop has no session to answer.
func TestARoleWithNoBusPushIsNeverChallenged(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.Hook(t, "m1", hook.Member)
	r.startHeartbeat(t)
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
	got, err := r.s.Pass(context.Background(), names, hook.BeatEvery, testDownAfter, next, by)
	require.NoError(t, err)
	for _, b := range got.Beats {
		if b.Did == hook.BeatChallenged {
			require.NoError(t, r.s.Delivered(context.Background(), b))
		}
	}
	return got
}

// TestAChangeNotMarkedToldIsFoundAgain pins mark-after-delivery at the
// store: until the server marks an audience told, every pass finds the same
// change for it; once marked, none.
func TestAChangeNotMarkedToldIsFoundAgain(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.Hook(t, "ada", hook.Comms)
	r.startHeartbeat(t)
	next := nonces()
	_, err := r.s.Unhook(context.Background(), "ada")
	require.NoError(t, err)
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
	r.startHeartbeat(t)
	r.Hook(t, "bob", hook.Comms)
	r.startHeartbeat(t)
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
	step()
	step()
	_, err := r.s.Unhook(context.Background(), "ada")
	require.NoError(t, err)
	_, err = r.s.Unhook(context.Background(), "bob")
	require.NoError(t, err)
	step() // both unhooked: tell the transition
	step()
	r.Hook(t, "ada", hook.Coordinator)
	r.startHeartbeat(t)
	step() // ada back, and the coordinator again
	step()
	assert.Equal(t, []told{
		{nil, nil, "ada", ""}, {nil, nil, "ada", "ada"},
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
	r.pass(hook.QuietAfter + time.Minute)
	b := r.beat(t, "srv", nonces())["ada"]
	require.NoError(t, r.s.Renew(ctx, "ada", r.state(t, "ada").Role, hook.Bus, b.Epoch, "holder"))
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
	r.startHeartbeat(t)
	want := hook.Want{Name: "ada", Need: []hook.Push{hook.Bus}}
	r.pass(hook.QuietAfter + time.Minute)
	b := r.beat(t, "srv", nonces())["ada"]
	require.NoError(t, r.s.Renew(ctx, "ada", r.state(t, "ada").Role, hook.Bus, b.Epoch, "holder"))
	_, err := r.s.Answer(ctx, "ada", b.Nonce, "session")
	require.NoError(t, err)
	r.pass(2 * hook.BeatEvery)
	r.Hook(t, "ada", hook.Comms)
	r.startHeartbeat(t) // its holder's renewal: the record itself is fresh
	require.NoError(t, r.s.Gate(ctx, want), "two intervals exactly: fresh")
	r.pass(time.Second)
	r.Hook(t, "ada", hook.Comms)
	r.startHeartbeat(t)
	err = r.s.Gate(ctx, want)
	var refusal *hook.Refusal
	require.ErrorAs(t, err, &refusal)
	assert.Contains(t, refusal.Why, "hook server stale")
	r.beat(t, "srv", nonces())
	assert.NoError(t, r.s.Gate(ctx, want), "the server passes again; no unnecessary challenge is required")
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
	r.startHeartbeat(t)
	require.NoError(t, r.Client.Del(ctx, hook.ServeKey).Err()) // the rig's evidence taken away: no server ever ran
	require.NoError(t, r.Client.HDel(ctx, hook.RecordKey("ada"), "answered_at").Err())
	want := hook.Want{Name: "ada", Need: []hook.Push{hook.Bus}}
	var refusal *hook.Refusal
	require.ErrorAs(t, r.s.Gate(ctx, want), &refusal)
	assert.Contains(t, refusal.Why, "hook server never ran")
	r.pass(hook.QuietAfter + time.Minute)
	b := r.beat(t, "srv", nonces())["ada"]
	require.NoError(t, r.s.Renew(ctx, "ada", hook.Comms, hook.Bus, b.Epoch, "holder"))
	require.ErrorAs(t, r.s.Gate(ctx, want), &refusal)
	assert.Contains(t, refusal.Why, "no answer to a heartbeat challenge")
	require.NoError(t, r.s.Renew(ctx, "ada", r.state(t, "ada").Role, hook.Bus, b.Epoch, "holder"))
	_, err := r.s.Answer(ctx, "ada", b.Nonce, "session")
	require.NoError(t, err)
	assert.NoError(t, r.s.Gate(ctx, want))
}

// TestAStaleAnswerIsDownWithAFreshServer pins the answer's own window
// (tla/HookBeat.tla; the model's BadUpAfterOutage): with the server passing
// every interval, a name whose last answer is older than the server's
// down-after by the store's TIME reads down, though it is still hooked.
func TestAStaleAnswerIsDownWithAFreshServer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	r.Hook(t, "ada", hook.Comms)
	r.startHeartbeat(t)
	r.pass(hook.QuietAfter + time.Minute)
	r.beatAfter(t, "srv", nonces(), hook.DownAfter)
	deadline := r.state(t, "ada").PendingUntil
	r.pass(hook.AnswerBound)
	require.NoError(t, r.s.Renew(ctx, "ada", hook.Comms, hook.Bus, r.state(t, "ada").Epoch, "holder"))
	require.NoError(t, r.Client.HSet(ctx, hook.ServeKey, "at", deadline.UnixMilli()).Err())
	assert.Empty(t, r.state(t, "ada").Down, "the full deadline is available")
	r.pass(time.Second)
	assert.Equal(t, hook.NoAnswerReason, r.state(t, "ada").Down)
}

// TestDownAfterIsTheReadersWindow pins that the readers' up (Served.Down)
// uses the server's down-after, written on its row by every pass, and two
// intervals for a row an older server wrote without one: a name the server
// keeps hooked does not read down in between.
func TestDownAfterIsTheReadersWindow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	r.Hook(t, "ada", hook.Comms)
	r.startHeartbeat(t)
	names, err := r.s.Names(ctx)
	require.NoError(t, err)
	_, err = r.s.Pass(ctx, names, hook.BeatEvery, hook.DownAfter, nonces(), "srv")
	require.NoError(t, err)
	sv, err := r.s.Serve(ctx)
	require.NoError(t, err)
	assert.Equal(t, hook.DownAfter, sv.DownAfter)
	assert.Equal(t, hook.DownAfter, sv.Window())
	st := hook.Status{Name: "ada", Role: hook.Comms, AnswerAt: sv.Now.Add(-4 * time.Minute)}
	assert.Empty(t, sv.Down(st), "answered four minutes ago, under a five-minute down-after: up")
	st.AnswerAt = sv.Now.Add(-hook.DownAfter - time.Second)
	st.PendingSent = true
	st.PendingUntil = sv.Now.Add(-time.Second)
	assert.Equal(t, hook.NoAnswerReason, sv.Down(st))
	require.NoError(t, r.Client.HDel(ctx, hook.ServeKey, "down_after").Err())
	sv, err = r.s.Serve(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2*hook.BeatEvery, sv.Window(), "an older server's row: its own two intervals")
}

func (r *rig) startHeartbeat(t *testing.T) {
	t.Helper()
	require.NoError(t, r.Client.HSet(context.Background(), hook.ServeKey, "by", "silence-test").Err())
}

// TestThePushedMarkIsTheStateAndTheCount pins the row's pushed mark
// (tla/HookBeat.tla, PushSend): a fresh row has none, the baseline writes the
// states found once and never over a mark that stands, a mark reads back as
// the state and the count, and a proven record with no heartbeat evidence is pushed as down.
func TestThePushedMarkIsTheStateAndTheCount(t *testing.T) {
	t.Parallel()
	rig := newRig(t)
	ctx := context.Background()
	got, seen, err := rig.s.Pushed(ctx)
	require.NoError(t, err)
	assert.False(t, seen)
	assert.Empty(t, got)
	require.NoError(t, rig.s.Baseline(ctx, map[string]string{"ada": "proven", "bob": "unhooked"}))
	require.NoError(t, rig.Client.HSet(ctx, hook.ServeKey, hook.PushMark("ada", "expired", 0).Field, hook.PushMark("ada", "expired", 0).Value).Err())
	require.NoError(t, rig.s.Baseline(ctx, map[string]string{"ada": "proven"}))
	got, seen, err = rig.s.Pushed(ctx)
	require.NoError(t, err)
	assert.True(t, seen)
	assert.Equal(t, hook.Pushed{State: "expired", Gen: 1}, got["ada"], "a baseline never replaces a mark")
	assert.Equal(t, hook.Pushed{State: "unhooked", Gen: 0}, got["bob"])
	assert.NotEqual(t, hook.PushOnce("ada", 1, "proven", 1), hook.PushOnce("ada", 1, "proven", 3), "a flap back to a state in one epoch is a push of its own")
	assert.Equal(t, hook.StateDown, hook.PushState(hook.Status{State: hook.StateProven, Down: hook.NoAnswerReason}))
	assert.Equal(t, hook.StateProven, hook.PushState(hook.Status{State: hook.StateProven}))
	assert.Equal(t, hook.StateExpired, hook.PushState(hook.Status{State: hook.StateExpired, Down: hook.StaleReason}))
}

// TestAProofGrantsAFreshSilenceWindow pins the silence rule's clock
// (tla/HookBeat.tla, NoChallengeWhileTalking): a busy friend who sends no bus
// message is challenged after BusyQuietAfter, proves, and is not challenged
// again until BusyQuietAfter after that proof, however many passes run between;
// the window counts from the later of her last send and her last proof.
func TestAProofGrantsAFreshSilenceWindow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	r.Hook(t, "ada", hook.Friend)
	r.startHeartbeat(t)
	next := nonces()
	pass := func() hook.Beat {
		names, err := r.s.Names(ctx)
		require.NoError(t, err)
		got, err := r.s.Pass(ctx, names, hook.BeatEvery, hook.DownAfter, next, "srv", "ada")
		require.NoError(t, err)
		for _, b := range got.Beats {
			if b.Did == hook.BeatChallenged {
				require.NoError(t, r.s.Delivered(ctx, b))
			}
			if b.Name == "ada" {
				return b
			}
		}
		return hook.Beat{}
	}
	r.pass(hook.BusyQuietAfter + time.Minute)
	b := pass()
	require.Equal(t, hook.BeatChallenged, b.Did)
	require.NoError(t, r.s.Renew(ctx, "ada", hook.Friend, hook.Bus, b.Epoch, "holder"))
	_, err := r.s.Answer(ctx, "ada", b.Nonce, "session")
	require.NoError(t, err)
	for range int(hook.BusyQuietAfter/hook.BeatEvery) - 1 { // one-minute passes up to the end of her window
		r.pass(hook.BeatEvery)
		assert.Equal(t, hook.BeatLeft, pass().Did)
	}
	r.pass(hook.BeatEvery)
	r.pass(hook.BeatEvery)
	assert.Equal(t, hook.BeatChallenged, pass().Did, "challenged again once the window from her proof has passed")
}

// TestAFreshlyArmedNameIsChallengedAtTheNextPass pins that arming is not
// activity (tla/HookBeat.tla, Pass): a name with no proof since its current
// arming is challenged at the very next pass, whatever its silence window; the
// window applies only once she has proved after the arming. Armed at t,
// challenged at the next pass; proven at t+1m, not challenged again before
// t+1m+QuietAfter, challenged at the pass after. A re-arm after an unhook is
// unproven again and is challenged at once.
func TestAFreshlyArmedNameIsChallengedAtTheNextPass(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	r.startHeartbeat(t)
	next := nonces()
	arm := func() {
		_, _, err := r.s.HookAs(ctx, "ada", hook.Comms, "ada")
		require.NoError(t, err)
	}
	prove := func(b hook.Beat) {
		require.NoError(t, r.s.Renew(ctx, "ada", hook.Comms, hook.Bus, b.Epoch, "holder"))
		_, err := r.s.Answer(ctx, "ada", b.Nonce, "session")
		require.NoError(t, err)
	}

	arm() // t
	b := r.beat(t, "srv", next)["ada"]
	require.Equal(t, hook.BeatChallenged, b.Did, "challenged at the next pass, with no silence window run")
	r.pass(time.Minute) // t+1m
	prove(b)
	for range int(hook.QuietAfter / hook.BeatEvery) { // t+2m ... t+1m+QuietAfter: silence not yet past the window
		r.pass(hook.BeatEvery)
		require.Equal(t, hook.BeatLeft, r.beatAfter(t, "srv", next, hook.DownAfter)["ada"].Did)
	}
	r.pass(hook.BeatEvery)
	b = r.beatAfter(t, "srv", next, hook.DownAfter)["ada"]
	require.Equal(t, hook.BeatChallenged, b.Did, "challenged once the window from her proof has passed")
	r.pass(time.Minute)
	prove(b)

	r.Unhook(t, "ada") // re-armed after an unhook: unproven again, challenged at once
	r.pass(time.Minute)
	arm()
	r.pass(hook.BeatEvery)
	assert.Equal(t, hook.BeatChallenged, r.beatAfter(t, "srv", next, hook.DownAfter)["ada"].Did)
}

// TestUpIsProvenNeverMerelyArmed pins who is up (tla/HookBeat.tla,
// NoBackWithoutProof and NoFlapWithoutProof; Glenn, 2026-10-16: only live
// when really up): a name its service arms and renews, with no model to
// prove, is hooked and live but down, so it is never back and never hears a
// broadcast; its lapse to unhooked changes nothing told; only the model's
// proof brings it back, once.
func TestUpIsProvenNeverMerelyArmed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	r.Hook(t, "ada", hook.Coordinator)
	r.Hook(t, "bob", hook.Comms)
	r.Unhook(t, "bob")
	next := nonces()
	tell := func() hook.Passed { // one pass, every audience marked told, the clock an interval on
		t.Helper()
		ada := r.state(t, "ada")
		require.NoError(t, r.s.Renew(ctx, "ada", hook.Coordinator, hook.Bus, ada.Epoch, "holder"))
		require.NoError(t, r.s.Renew(ctx, "ada", hook.Coordinator, hook.Sprint, ada.Epoch, "holder"))
		p := r.passAll(t, "srv", next)
		require.True(t, p.Taken)
		for _, a := range hook.Audiences {
			require.NoError(t, r.s.Told(ctx, p, a))
		}
		r.pass(hook.BeatEvery)
		return p
	}
	p := tell()
	assert.Equal(t, []string{"bob"}, p.Unhooked, "bob is down")

	e, _, err := r.s.HookAs(ctx, "bob", hook.Comms, "service") // the service re-arms bob: no model behind it
	require.NoError(t, err)
	var challenge hook.Beat
	for i := range 2 {
		require.NoError(t, r.s.Renew(ctx, "bob", hook.Comms, hook.Bus, e, "service"))
		require.Equal(t, hook.StateLive, r.state(t, "bob").State)
		p := tell()
		for _, b := range p.Beats {
			if b.Name == "bob" && b.Did == hook.BeatChallenged {
				challenge = b
			}
		}
		_, back, _ := p.Diff(hook.ToCoordinator)
		assert.Empty(t, back, "pass %d: armed is not back", i)
		assert.Equal(t, []string{"bob"}, p.Unhooked, "pass %d: hooked and live, not proven: down", i)
		assert.Equal(t, []string{"ada"}, p.Live, "pass %d: the armed name hears no broadcast", i)
		assert.Equal(t, "ada", p.Coord)
	}
	require.NotEmpty(t, challenge.Nonce, "the armed name is challenged at once")

	for i := 0; r.state(t, "bob").State != hook.StateUnhooked; i++ { // unanswered past its answer bound and the down-after: unhooked again
		require.Less(t, i, 10, "the unanswered challenge unhooks bob")
		require.NoError(t, r.s.Renew(ctx, "bob", hook.Comms, hook.Bus, e, "service"))
		p = tell()
		assert.False(t, p.Changed(hook.ToCoordinator), "pass %d: a lapse from live to unhooked while down is no change", i)
		assert.False(t, p.Changed(hook.ToRest), "pass %d", i)
	}
	p = tell()
	assert.False(t, p.Changed(hook.ToCoordinator), "unhooked while down: nothing told")

	e, _, err = r.s.HookAs(ctx, "bob", hook.Comms, "service") // armed again; this time the model answers
	require.NoError(t, err)
	require.NoError(t, r.s.Renew(ctx, "bob", hook.Comms, hook.Bus, e, "service"))
	p = tell()
	var b hook.Beat
	for _, x := range p.Beats {
		if x.Name == "bob" {
			b = x
		}
	}
	require.Equal(t, hook.BeatChallenged, b.Did)
	assert.False(t, p.Changed(hook.ToCoordinator), "armed again: still down")
	_, err = r.s.Answer(ctx, "bob", b.Nonce, "session")
	require.NoError(t, err)
	require.Equal(t, hook.StateProven, r.state(t, "bob").State)
	p = tell()
	down, back, _ := p.Diff(hook.ToCoordinator)
	assert.Empty(t, down)
	assert.Equal(t, []string{"bob"}, back, "proven: back")
	assert.Empty(t, p.Unhooked)
	assert.Equal(t, []string{"ada", "bob"}, p.Live)
	require.NoError(t, r.s.Renew(ctx, "bob", hook.Comms, hook.Bus, e, "service"))
	p = tell()
	assert.False(t, p.Changed(hook.ToCoordinator), "back once")
}
