package hook_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/hook/hooktest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rig is the hooktest rig with the names this file's tests use.
type rig struct {
	*hooktest.Rig
	mr *miniredis.Miniredis
	s  hook.Store
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := hooktest.New(t)
	return &rig{Rig: r, mr: r.MR, s: r.Store}
}

func (r *rig) pass(d time.Duration) { r.Pass(d) }

func (r *rig) state(t *testing.T, name string) hook.Status { return r.State(t, name) }

// TestArmJoinsAndUnhookClears pins the lifecycle on the store
// (tla/Hook.tla): arm starts epoch 1; a second arm as the same role joins it
// and keeps its leases; arm as another role starts a new epoch and clears
// them, and only a fresh arm does that (a holder's arm as another role is
// refused); unhook moves the epoch on and clears every key, and a holder's
// renew under the old epoch is refused after it.
func TestArmJoinsAndUnhookClears(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	e, joined, err := r.s.Arm(ctx, "ada", hook.Coordinator, "test", false)
	require.NoError(t, err)
	assert.Equal(t, int64(1), e)
	assert.False(t, joined)
	require.NoError(t, r.s.Renew(ctx, "ada", hook.Coordinator, hook.Bus, e, "hook"))
	assert.Equal(t, hook.StatePartial, r.state(t, "ada").State)

	e2, joined, err := r.s.Arm(ctx, "ada", hook.Coordinator, "test", false)
	require.NoError(t, err)
	assert.Equal(t, e, e2)
	assert.True(t, joined)
	assert.Equal(t, hook.StatePartial, r.state(t, "ada").State, "a join keeps the leases")

	_, _, err = r.s.Arm(ctx, "ada", hook.Comms, "a daemon", false)
	require.ErrorIs(t, err, hook.ErrOtherRole, "a holder never takes a record hooked as another role")
	assert.Equal(t, hook.StatePartial, r.state(t, "ada").State, "and the record is left as it was")
	e3, joined, err := r.s.Arm(ctx, "ada", hook.Comms, "test", true)
	require.NoError(t, err)
	assert.Equal(t, int64(2), e3)
	assert.False(t, joined)
	assert.Equal(t, hook.StateExpired, r.state(t, "ada").State, "a new role clears the leases")
	assert.ErrorIs(t, r.s.Renew(ctx, "ada", hook.Comms, hook.Bus, e, "old"), hook.ErrNotHooked, "a holder of the old epoch writes nothing")

	require.NoError(t, r.s.Renew(ctx, "ada", hook.Comms, hook.Bus, e3, "hook"))
	was, err := r.s.Unhook(ctx, "ada")
	require.NoError(t, err)
	assert.Equal(t, "hooked", was)
	st := r.state(t, "ada")
	assert.Equal(t, hook.StateUnhooked, st.State)
	assert.False(t, st.Unhooked.IsZero())
	assert.False(t, r.mr.Exists(hook.LiveKey("ada", hook.Bus)), "unhook clears the lease")
	assert.ErrorIs(t, r.s.Renew(ctx, "ada", hook.Comms, hook.Bus, e3, "hook"), hook.ErrNotHooked)

	was, err = r.s.Unhook(ctx, "ada")
	require.NoError(t, err)
	assert.Equal(t, hook.StateUnhooked, was)
	was, err = r.s.Unhook(ctx, "bob")
	require.NoError(t, err)
	assert.Equal(t, hook.StateNone, was)

	names, err := r.s.Names(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"ada"}, names)
}

// TestALeaseExpiresOnItsOwn pins liveness by TTL: a holder that stops
// renewing loses its push after hook.LeaseTTL with nobody writing, and the gate
// refuses (tla/Hook.tla, Expire).
func TestALeaseExpiresOnItsOwn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	h := &hook.Holder{Store: r.s, Name: "m1", Role: hook.Member, Pushes: []hook.Push{hook.Work}, By: "loop"}
	require.NoError(t, h.Start(ctx))
	require.NoError(t, h.Beat(ctx, map[hook.Push]bool{hook.Work: true}))
	r.Evidence(t, "m1") // a heartbeat server passing (hook.Served.Down)
	require.NoError(t, h.Gate(ctx, hook.Work))

	r.pass(hook.RenewEvery)
	require.NoError(t, h.Beat(ctx, map[hook.Push]bool{hook.Work: true}))
	r.pass(hook.LeaseTTL - time.Second)
	assert.NoError(t, h.Gate(ctx, hook.Work), "inside the lease the push stays live")

	r.pass(2 * time.Second)
	var refused *hook.Refusal
	require.ErrorAs(t, h.Gate(ctx, hook.Work), &refused)
	assert.Equal(t, hook.StateExpired, refused.Status.State)
	assert.Equal(t, "nova-hook hook --as m1 --role member", refused.Hook)
}

// TestAChallengeProvesOnlyWithItsNonce pins the session's proof
// (tla/Hook.tla, Challenge and Answer): the store keeps the nonce's sum; a
// wrong nonce, a lapsed challenge and a challenge of an old epoch prove
// nothing; the right nonce proves the live pushes it named and is spent.
func TestAChallengeProvesOnlyWithItsNonce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	e, _, err := r.s.Arm(ctx, "ada", hook.Comms, "test", false)
	require.NoError(t, err)
	require.NoError(t, r.s.Renew(ctx, "ada", hook.Comms, hook.Bus, e, "hook"))

	_, err = r.s.Answer(ctx, "ada", "n1", "session")
	assert.ErrorIs(t, err, hook.ErrNoChallenge)

	created, challengeErr := r.s.Challenge(ctx, "ada", hook.Comms, e, []hook.Push{hook.Bus}, "n1")
	require.NoError(t, challengeErr)
	require.True(t, created)
	assert.NotContains(t, r.mr.HGet(hook.ChallengeKey("ada"), "sum"), "n1", "the store keeps the sum, never the nonce")
	_, err = r.s.Answer(ctx, "ada", "n2", "session")
	assert.ErrorIs(t, err, hook.ErrWrongNonce)
	assert.Equal(t, hook.StateLive, r.state(t, "ada").State)

	n, err := r.s.Answer(ctx, "ada", "n1", "session")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, hook.StateProven, r.state(t, "ada").State)
	_, err = r.s.Answer(ctx, "ada", "n1", "session")
	assert.ErrorIs(t, err, hook.ErrNoChallenge, "an answered challenge is spent")

	created, challengeErr = r.s.Challenge(ctx, "ada", hook.Comms, e, []hook.Push{hook.Bus}, "n3")
	require.NoError(t, challengeErr)
	require.True(t, created)
	r.pass(hook.AnswerBound + time.Second)
	_, err = r.s.Answer(ctx, "ada", "n3", "session")
	assert.ErrorIs(t, err, hook.ErrNoChallenge, "a challenge lapses past its bound")

	require.NoError(t, r.s.Renew(ctx, "ada", hook.Comms, hook.Bus, e, "hook"))
	created, challengeErr = r.s.Challenge(ctx, "ada", hook.Comms, e, []hook.Push{hook.Bus}, "n4")
	require.NoError(t, challengeErr)
	require.True(t, created)
	_, err = r.s.Unhook(ctx, "ada")
	require.NoError(t, err)
	_, err = r.s.Answer(ctx, "ada", "n4", "session")
	assert.ErrorIs(t, err, hook.ErrNoChallenge, "unhook clears the challenge")
	_, challengeErr = r.s.Challenge(ctx, "ada", hook.Comms, e, []hook.Push{hook.Bus}, "n5")
	assert.ErrorIs(t, challengeErr, hook.ErrNotHooked)
}

// TestAProofNeedsALiveLease pins that nothing proves a push nobody holds
// (tla/Hook.tla, ProvenIsHeld), and that a proof lapses after hook.ProofTTL.
func TestAProofNeedsALiveLease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	e, _, err := r.s.Arm(ctx, "bob", hook.Friend, "test", false)
	require.NoError(t, err)
	err = r.s.Prove(ctx, "bob", hook.Friend, hook.Work, e, "lanes")
	require.Error(t, err)
	assert.False(t, errors.Is(err, hook.ErrNotHooked))
	assert.Contains(t, err.Error(), "holds no live lease")

	created, challengeErr := r.s.Challenge(ctx, "bob", hook.Friend, e, []hook.Push{hook.Bus, hook.Work}, "n")
	require.NoError(t, challengeErr)
	require.True(t, created)
	n, err := r.s.Answer(ctx, "bob", "n", "session")
	require.NoError(t, err)
	assert.Equal(t, 0, n, "an answer proves no push whose lease is down")

	h := &hook.Holder{Store: r.s, Name: "bob", Role: hook.Friend, Pushes: []hook.Push{hook.Bus}, By: "daemon"}
	require.NoError(t, h.Start(ctx))
	require.NoError(t, h.Beat(ctx, map[hook.Push]bool{hook.Bus: true}))
	assert.Equal(t, hook.StateProven, r.state(t, "bob").State)
	require.NoError(t, h.Beat(ctx, map[hook.Push]bool{}))
	assert.Equal(t, hook.StateLive, r.state(t, "bob").State, "an owner that says unproven clears the proof")
	require.NoError(t, h.Beat(ctx, map[hook.Push]bool{hook.Bus: true}))
	for range 4 {
		r.pass(hook.RenewEvery)
		require.NoError(t, r.s.Renew(ctx, "bob", hook.Friend, hook.Bus, h.Epoch(), "daemon"))
	}
	assert.Equal(t, hook.StateProven, r.state(t, "bob").State)
	r.pass(hook.ProofTTL)
	require.NoError(t, r.s.Renew(ctx, "bob", hook.Friend, hook.Bus, h.Epoch(), "daemon"))
	assert.Equal(t, hook.StateLive, r.state(t, "bob").State, "a proof lapses after hook.ProofTTL")
}

// TestAHolderRespectsAnUnhook pins that an unhook stands against a running
// holder: its beat answers hook.ErrNotHooked until someone hooks the identity in
// again as the holder's role, and then it rejoins that epoch.
func TestAHolderRespectsAnUnhook(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	h := &hook.Holder{Store: r.s, Name: "bob", Role: hook.Friend, Pushes: []hook.Push{hook.Bus}, By: "daemon"}
	require.NoError(t, h.Start(ctx))
	_, err := r.s.Unhook(ctx, "bob")
	require.NoError(t, err)
	assert.ErrorIs(t, h.Beat(ctx, map[hook.Push]bool{hook.Bus: true}), hook.ErrNotHooked)
	assert.Equal(t, hook.StateUnhooked, r.state(t, "bob").State, "the holder wrote nothing")

	_, _, err = r.s.Arm(ctx, "bob", hook.Friend, "a holder", false)
	require.ErrorIs(t, err, hook.ErrNotHooked, "a holder's arm never undoes an unhook")
	e, joined, err := r.s.HookAs(ctx, "bob", hook.Friend, "nova-hook")
	require.NoError(t, err)
	assert.False(t, joined)
	require.NoError(t, h.Beat(ctx, map[hook.Push]bool{hook.Bus: true}))
	assert.Equal(t, e, h.Epoch())
	assert.Equal(t, hook.StateProven, r.state(t, "bob").State)
	r.Evidence(t, "bob") // a heartbeat server passing and her answer (hook.Served.Down)
	assert.NoError(t, h.Gate(ctx, hook.Bus))
}

// TestReadIsOneRoundTripForAnyNames pins that status reads every name in one
// pipeline, and that a name refused by the gate is never read as a pass when
// the store fails (fail closed).
func TestReadIsOneRoundTripForAnyNames(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	_, _, err := r.s.Arm(ctx, "ada", hook.Coordinator, "test", false)
	require.NoError(t, err)
	before := r.mr.CommandCount()
	st, now, err := r.s.Read(ctx, "ada", "bob", "carol")
	require.NoError(t, err)
	assert.Len(t, st, 3)
	assert.Equal(t, 2+3*(1+4*len(hook.Pushes)), r.mr.CommandCount()-before, "one pipeline: TIME, the heartbeat server's row, and per name a record and four reads per push")
	assert.Equal(t, 2026, now.Year())
	assert.Equal(t, []string{hook.StateExpired, hook.StateNone, hook.StateNone}, []string{st[0].State, st[1].State, st[2].State})

	r.mr.SetError("LOADING the store is down")
	err = r.s.Gate(ctx, hook.Want{Name: "ada", Need: []hook.Push{hook.Bus}})
	require.Error(t, err)
	var refused *hook.Refusal
	assert.False(t, errors.As(err, &refused), "a store error is its own refusal, never a gate's pass")
	assert.Contains(t, err.Error(), "fail closed")
}

// TestNamesAreRefusedBeforeAnyWrite pins that a name the bus would refuse is
// refused before a script runs.
func TestNamesAreRefusedBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	for _, n := range []string{"", "Ada", "a b", "x:y"} {
		_, _, err := r.s.Arm(ctx, n, hook.Comms, "test", false)
		assert.Error(t, err, n)
		_, err = r.s.Unhook(ctx, n)
		assert.Error(t, err, n)
		assert.Error(t, r.s.Gate(ctx, hook.Want{Name: n}), n)
	}
	_, _, err := r.s.Arm(ctx, "ada", hook.Role("seat"), "test", false)
	assert.ErrorContains(t, err, "is no role")
	assert.Empty(t, r.mr.Keys())
}

// TestAnUnhookStandsAcrossARestart pins that a holder's start never undoes an
// unhook (tla/Hook.tla, Arm only from none; HookedByItsWord): a daemon
// restarted after its friend was unhooked holds nothing and the record stays
// unhooked; the identity's own word (nova-hook hook) hooks it in again, and
// the restarted holder then joins.
func TestAnUnhookStandsAcrossARestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	first := &hook.Holder{Store: r.s, Name: "bob", Role: hook.Friend, Pushes: []hook.Push{hook.Bus, hook.Work}, By: "daemon pid 1"}
	require.NoError(t, first.Start(ctx))
	_, err := r.s.Unhook(ctx, "bob")
	require.NoError(t, err)

	restarted := &hook.Holder{Store: r.s, Name: "bob", Role: hook.Friend, Pushes: []hook.Push{hook.Bus, hook.Work}, By: "daemon pid 2"}
	require.ErrorIs(t, restarted.Start(ctx), hook.ErrNotHooked)
	assert.Equal(t, hook.StateUnhooked, r.state(t, "bob").State, "the restart did not hook bob in")
	assert.Zero(t, restarted.Epoch())
	assert.False(t, r.mr.Exists(hook.LiveKey("bob", hook.Bus)), "and wrote no lease")

	e, _, err := r.s.HookAs(ctx, "bob", hook.Friend, "nova-hook")
	require.NoError(t, err)
	require.NoError(t, restarted.Start(ctx))
	assert.Equal(t, e, restarted.Epoch(), "hooked in again, the restarted holder joins")
	require.NoError(t, restarted.Beat(ctx, map[hook.Push]bool{hook.Bus: true, hook.Work: true}))
	assert.Equal(t, hook.StateProven, r.state(t, "bob").State)
}

// TestALostRecordReArms pins that a record lost under a running holder (its
// key deleted, or the store flushed) is armed again by the holder's next beat
// (tla/Hook.tla, Flush then Arm): none is not an unhook.
func TestALostRecordReArms(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	h := &hook.Holder{Store: r.s, Name: "bob", Role: hook.Friend, Pushes: []hook.Push{hook.Bus, hook.Work}, By: "daemon"}
	require.NoError(t, h.Start(ctx))
	proven := map[hook.Push]bool{hook.Bus: true, hook.Work: true}
	require.NoError(t, h.Beat(ctx, proven))

	r.mr.FlushAll()
	assert.Equal(t, hook.StateNone, r.state(t, "bob").State)
	require.NoError(t, h.Beat(ctx, proven))
	assert.Equal(t, hook.StateProven, r.state(t, "bob").State, "the running holder armed the lost record again")
	assert.Equal(t, hook.Friend, r.state(t, "bob").Role)

	r.mr.Del(hook.RecordKey("bob"))
	require.NoError(t, h.Beat(ctx, proven))
	assert.Equal(t, hook.StateProven, r.state(t, "bob").State, "a record deleted alone is armed again too")
}

// TestAHolderOfAnotherRoleWritesNothingAfterALoss pins the role in every
// holder's write: a record lost and armed again from nothing counts its epoch
// from 1 again, and a holder of another role that joined the old epoch 1
// writes nothing under the new one (tla/Hook.tla, Renew's role).
func TestAHolderOfAnotherRoleWritesNothingAfterALoss(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	seat := &hook.Holder{Store: r.s, Name: "ada", Role: hook.Coordinator, Pushes: []hook.Push{hook.Bus}, By: "session"}
	require.NoError(t, seat.Start(ctx))
	r.mr.FlushAll()
	daemon := &hook.Holder{Store: r.s, Name: "ada", Role: hook.Friend, Pushes: []hook.Push{hook.Bus, hook.Work}, By: "daemon"}
	require.NoError(t, daemon.Start(ctx))
	require.Equal(t, seat.Epoch(), daemon.Epoch(), "the epoch counts from 1 again")
	assert.ErrorIs(t, r.s.Renew(ctx, "ada", hook.Coordinator, hook.Bus, seat.Epoch(), "session"), hook.ErrNotHooked)
	assert.ErrorIs(t, r.s.Prove(ctx, "ada", hook.Coordinator, hook.Bus, seat.Epoch(), "session"), hook.ErrNotHooked)
	_, challengeErr := r.s.Challenge(ctx, "ada", hook.Coordinator, seat.Epoch(), []hook.Push{hook.Bus}, "n")
	assert.ErrorIs(t, challengeErr, hook.ErrNotHooked)
	assert.ErrorIs(t, seat.Beat(ctx, map[hook.Push]bool{hook.Bus: true}), hook.ErrNotHooked, "the seat's holder never rejoins a record of another role")
}

// TestEachRoundTripHasItsOwnTimeout pins Store.Timeout: a store that does not
// answer refuses each call within its own bound, not the caller's whole
// context.
func TestEachRoundTripHasItsOwnTimeout(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	s := hook.Store{C: r.Client, Timeout: 50 * time.Millisecond}
	ctx := context.Background()
	_, _, err := s.Arm(ctx, "ada", hook.Comms, "test", false)
	require.NoError(t, err)
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	r.mr.Server().SetPreHook(func(c *server.Peer, cmd string, args ...string) bool {
		if cmd == "EVALSHA" || cmd == "EVAL" {
			<-blocked
		}
		return false
	})
	start := time.Now()
	err = s.Renew(ctx, "ada", hook.Comms, hook.Bus, 1, "test")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 5*time.Second, "the call's own bound, not the caller's")
}

// TestBootstrapPreservesHeartbeatOwnership pins Bootstrap in tla/HookBeat.tla:
// startup cannot replace a delivered nonce or its deadline; after the answer
// or expiry, beat_at still gives this epoch to the heartbeat.
func TestBootstrapPreservesHeartbeatOwnership(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"open", "late", "answered", "expired"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			r := newRig(t)
			e, _, err := r.s.Arm(ctx, "ada", hook.Comms, "test", false)
			require.NoError(t, err)
			require.NoError(t, r.s.Renew(ctx, "ada", hook.Comms, hook.Bus, e, "hook"))
			b := r.beat(t, "server", nonces())["ada"]
			require.Equal(t, hook.BeatChallenged, b.Did)
			switch state {
			case "late":
				r.pass(hook.BeatEvery + time.Millisecond)
			case "answered":
				_, err = r.s.Answer(ctx, "ada", b.Nonce, "session")
				require.NoError(t, err)
			case "expired":
				r.pass(2*hook.BeatEvery + hook.AnswerBound + time.Millisecond)
			}
			before, err := r.Client.HGetAll(ctx, hook.ChallengeKey("ada")).Result()
			require.NoError(t, err)
			ttl := r.mr.TTL(hook.ChallengeKey("ada"))
			created, err := r.s.Challenge(ctx, "ada", hook.Comms, e, []hook.Push{hook.Bus}, "client")
			require.NoError(t, err)
			require.False(t, created, "the client waits for the next server delivery")
			after, err := r.Client.HGetAll(ctx, hook.ChallengeKey("ada")).Result()
			require.NoError(t, err)
			assert.Equal(t, before, after, "including the nonce sum and until")
			assert.Equal(t, ttl, r.mr.TTL(hook.ChallengeKey("ada")), "skip does not extend a deadline")
			if state == "open" {
				n, err := r.s.Answer(ctx, "ada", b.Nonce, "session")
				require.NoError(t, err, "the nonce already delivered by the server still answers")
				assert.Equal(t, 1, n)
				r.pass(hook.BeatEvery)
				next := r.beat(t, "server", nonces())["ada"]
				assert.Zero(t, next.Misses, "a client startup cannot cause a counted miss")
			}
			if state == "late" {
				_, err := r.s.Answer(ctx, "ada", b.Nonce, "session")
				assert.NoError(t, err, "past its interval and inside the down-after window, the server nonce still answers")
			}
			e2, _, err := r.s.Arm(ctx, "ada", hook.Comms, "test", true)
			require.NoError(t, err)
			created, err = r.s.Challenge(ctx, "ada", hook.Comms, e, []hook.Push{hook.Bus}, "stale")
			require.ErrorIs(t, err, hook.ErrNotHooked)
			assert.False(t, created)
			created, err = r.s.Challenge(ctx, "ada", hook.Comms, e2, []hook.Push{hook.Bus}, "fresh")
			require.NoError(t, err)
			assert.True(t, created, "a fresh epoch clears heartbeat ownership")
		})
	}
}

// TestBootstrapKeepsTheFirstClientNonce pins the pre-heartbeat branch of
// Bootstrap in tla/HookBeat.tla: concurrent starters cannot replace the winner.
func TestBootstrapKeepsTheFirstClientNonce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	e, _, err := r.s.Arm(ctx, "ada", hook.Comms, "test", false)
	require.NoError(t, err)
	created, err := r.s.Challenge(ctx, "ada", hook.Comms, e, []hook.Push{hook.Bus}, "first")
	require.NoError(t, err)
	require.True(t, created)
	created, err = r.s.Challenge(ctx, "ada", hook.Comms, e, []hook.Push{hook.Bus}, "second")
	require.NoError(t, err)
	assert.False(t, created)
	_, err = r.s.Answer(ctx, "ada", "second", "session")
	assert.ErrorIs(t, err, hook.ErrWrongNonce)
	_, err = r.s.Answer(ctx, "ada", "first", "session")
	require.NoError(t, err)
}

// TestPlanPassIsThePassWithItsWritesOff pins Store.PlanPass: the beats Pass
// then carries out, and no write to the store before it.
func TestPlanPassIsThePassWithItsWritesOff(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.Hook(t, "ada", hook.Comms)
	ctx := context.Background()
	before := r.mr.Dump()
	plan, err := r.s.PlanPass(ctx, []string{"ada"}, hook.BeatEvery, hook.DownAfter, "srv")
	require.NoError(t, err)
	require.True(t, plan.Taken)
	require.Len(t, plan.Beats, 1)
	assert.Equal(t, hook.BeatChallenged, plan.Beats[0].Did)
	assert.Empty(t, plan.Beats[0].Nonce, "a plan sets no challenge")
	assert.Equal(t, before, r.mr.Dump(), "a plan writes nothing")

	got, err := r.s.Pass(ctx, []string{"ada"}, hook.BeatEvery, hook.DownAfter, func() string { return "n1" }, "srv")
	require.NoError(t, err)
	require.True(t, got.Taken, "the plan left the interval free")
	assert.Equal(t, plan.Beats[0].Did, got.Beats[0].Did)
	assert.Equal(t, plan.Beats[0].Misses, got.Beats[0].Misses)
	assert.Equal(t, plan.Beats[0].Epoch, got.Beats[0].Epoch)
}
