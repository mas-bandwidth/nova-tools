package hook_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
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
	require.NoError(t, r.s.Renew(ctx, "ada", hook.Bus, e, "hook"))
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
	assert.ErrorIs(t, r.s.Renew(ctx, "ada", hook.Bus, e, "old"), hook.ErrNotHooked, "a holder of the old epoch writes nothing")

	require.NoError(t, r.s.Renew(ctx, "ada", hook.Bus, e3, "hook"))
	was, err := r.s.Unhook(ctx, "ada")
	require.NoError(t, err)
	assert.Equal(t, "hooked", was)
	st := r.state(t, "ada")
	assert.Equal(t, hook.StateUnhooked, st.State)
	assert.False(t, st.Unhooked.IsZero())
	assert.False(t, r.mr.Exists(hook.LiveKey("ada", hook.Bus)), "unhook clears the lease")
	assert.ErrorIs(t, r.s.Renew(ctx, "ada", hook.Bus, e3, "hook"), hook.ErrNotHooked)

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
	require.NoError(t, r.s.Renew(ctx, "ada", hook.Bus, e, "hook"))

	_, err = r.s.Answer(ctx, "ada", "n1", "session")
	assert.ErrorIs(t, err, hook.ErrNoChallenge)

	require.NoError(t, r.s.Challenge(ctx, "ada", e, []hook.Push{hook.Bus}, "n1"))
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

	require.NoError(t, r.s.Challenge(ctx, "ada", e, []hook.Push{hook.Bus}, "n3"))
	r.pass(hook.AnswerBound + time.Second)
	_, err = r.s.Answer(ctx, "ada", "n3", "session")
	assert.ErrorIs(t, err, hook.ErrNoChallenge, "a challenge lapses past its bound")

	require.NoError(t, r.s.Renew(ctx, "ada", hook.Bus, e, "hook"))
	require.NoError(t, r.s.Challenge(ctx, "ada", e, []hook.Push{hook.Bus}, "n4"))
	_, err = r.s.Unhook(ctx, "ada")
	require.NoError(t, err)
	_, err = r.s.Answer(ctx, "ada", "n4", "session")
	assert.ErrorIs(t, err, hook.ErrNoChallenge, "unhook clears the challenge")
	assert.ErrorIs(t, r.s.Challenge(ctx, "ada", e, []hook.Push{hook.Bus}, "n5"), hook.ErrNotHooked)
}

// TestAProofNeedsALiveLease pins that nothing proves a push nobody holds
// (tla/Hook.tla, ProvenIsHeld), and that a proof lapses after hook.ProofTTL.
func TestAProofNeedsALiveLease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	e, _, err := r.s.Arm(ctx, "bob", hook.Friend, "test", false)
	require.NoError(t, err)
	err = r.s.Prove(ctx, "bob", hook.Work, e, "lanes")
	require.Error(t, err)
	assert.False(t, errors.Is(err, hook.ErrNotHooked))
	assert.Contains(t, err.Error(), "holds no live lease")

	require.NoError(t, r.s.Challenge(ctx, "bob", e, []hook.Push{hook.Bus, hook.Work}, "n"))
	n, err := r.s.Answer(ctx, "bob", "n", "session")
	require.NoError(t, err)
	assert.Equal(t, 0, n, "an answer proves no push whose lease is down")

	h := &hook.Holder{Store: r.s, Name: "bob", Role: hook.Friend, Pushes: []hook.Push{hook.Bus, hook.Work}, By: "daemon"}
	require.NoError(t, h.Start(ctx))
	require.NoError(t, h.Beat(ctx, map[hook.Push]bool{hook.Bus: true, hook.Work: true}))
	assert.Equal(t, hook.StateProven, r.state(t, "bob").State)
	require.NoError(t, h.Beat(ctx, map[hook.Push]bool{hook.Bus: true}))
	assert.Equal(t, hook.StateLive, r.state(t, "bob").State, "an owner that says unproven clears the proof")
	require.NoError(t, h.Beat(ctx, map[hook.Push]bool{hook.Bus: true, hook.Work: true}))
	for range 4 {
		r.pass(hook.RenewEvery)
		require.NoError(t, r.s.Renew(ctx, "bob", hook.Bus, h.Epoch(), "daemon"))
		require.NoError(t, r.s.Renew(ctx, "bob", hook.Work, h.Epoch(), "daemon"))
	}
	assert.Equal(t, hook.StateProven, r.state(t, "bob").State)
	r.pass(hook.ProofTTL)
	require.NoError(t, r.s.Renew(ctx, "bob", hook.Bus, h.Epoch(), "daemon"))
	require.NoError(t, r.s.Renew(ctx, "bob", hook.Work, h.Epoch(), "daemon"))
	assert.Equal(t, hook.StateLive, r.state(t, "bob").State, "a proof lapses after hook.ProofTTL")
}

// TestAHolderRespectsAnUnhook pins that an unhook stands against a running
// holder: its beat answers hook.ErrNotHooked until someone hooks the identity in
// again as the holder's role, and then it rejoins that epoch.
func TestAHolderRespectsAnUnhook(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	h := &hook.Holder{Store: r.s, Name: "bob", Role: hook.Friend, Pushes: []hook.Push{hook.Bus, hook.Work}, By: "daemon"}
	require.NoError(t, h.Start(ctx))
	_, err := r.s.Unhook(ctx, "bob")
	require.NoError(t, err)
	assert.ErrorIs(t, h.Beat(ctx, map[hook.Push]bool{hook.Bus: true, hook.Work: true}), hook.ErrNotHooked)
	assert.Equal(t, hook.StateUnhooked, r.state(t, "bob").State, "the holder wrote nothing")

	e, joined, err := r.s.Arm(ctx, "bob", hook.Friend, "nova-hook", false)
	require.NoError(t, err)
	assert.False(t, joined)
	require.NoError(t, h.Beat(ctx, map[hook.Push]bool{hook.Bus: true, hook.Work: true}))
	assert.Equal(t, e, h.Epoch())
	assert.Equal(t, hook.StateProven, r.state(t, "bob").State)
	assert.NoError(t, h.Gate(ctx, hook.Bus, hook.Work))
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
	assert.Equal(t, 1+3*(1+4*len(hook.Pushes)), r.mr.CommandCount()-before, "one pipeline: TIME and per name a record and four reads per push")
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
