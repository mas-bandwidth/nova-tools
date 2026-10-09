package friend

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// proverRig is the presence rig with the daemon's push proof wired as the
// daemon wires it: the SessionCheck's Save is the prover's.
type proverRig struct {
	*presenceRig
	prover *PushProver
	mu     sync.Mutex
	lines  []string
}

func newProverRig(t *testing.T, harness Deliverer) *proverRig {
	t.Helper()
	r := &proverRig{presenceRig: newPresenceRig(t)}
	if harness != nil {
		r.sc.Deliver = r.sc.Gate(harness)
	}
	r.prover = &PushProver{Friend: "bob", Harness: "claude", Store: r.store, Deliver: r.sc.Deliver,
		Now: func() time.Time { return r.now }, Record: func(l string) { r.mu.Lock(); r.lines = append(r.lines, l); r.mu.Unlock() }}
	r.sc.Save = r.prover.Save(nil)
	return r
}

// proof is bob's proof on the bus as nova-bus reads it, and the store's now.
func (r *proverRig) proof(t *testing.T) (bus.PushProof, time.Time) {
	t.Helper()
	ps, now, err := (&bus.Bus{Store: r.store}).PushProofs(context.Background(), "bob")
	require.NoError(t, err)
	return ps[0], now
}

// TestTheSessionChecksPongIsThePushProof: the friend daemon's SESSION CHECK
// round trip (the check carried in by the deliver adapter, the session's
// pong carrying its nonce) is what nova-bus reads as bob's inbox push. A
// daemon that started proves nothing (down, deaf); the answer writes the
// proof up with the nonce and the harness; while the session stays up the
// daemon renews it every PushRenewEvery, so it never reads stale; a check
// unanswered within its bound writes it down at once; and a daemon that
// stops renewing leaves a proof that goes stale at bus.PushFresh.
func TestTheSessionChecksPongIsThePushProof(t *testing.T) {
	t.Parallel()
	r := newProverRig(t, nil)
	ctx := context.Background()
	heard := func() error { return (&bus.Bus{Store: r.store}).Heard(ctx, "bob") }

	p, now := r.proof(t)
	assert.Equal(t, bus.PushNone, p.State(now), "no daemon has written one")
	r.step(t, BeatEvery)
	p, now = r.proof(t)
	assert.Equal(t, bus.PushDown, p.State(now), "a daemon that started proves nothing about the session")
	assert.Equal(t, NotYetAnswered, p.Reason)
	require.Error(t, heard())
	assert.Contains(t, heard().Error(), "deaf: bob has no proven push since")

	r.send(t, r.daemon, "bob", DaemonPongSubject, "daemon-pong n1\n")
	r.step(t, BeatEvery)
	p, now = r.proof(t)
	assert.Equal(t, bus.PushDown, p.State(now), "the daemon's own pong proves no push")

	r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 4)+"\n")
	r.step(t, BeatEvery)
	p, now = r.proof(t)
	assert.Equal(t, bus.PushProven, p.State(now), "the session's answer to the check is the proof")
	assert.Equal(t, "n1", p.Nonce)
	assert.Equal(t, "claude", p.Harness)
	assert.Equal(t, r.now, p.Proven)
	require.NoError(t, heard())

	// the session talks on the bus, so no new check goes in; the daemon renews
	first := p.At
	for range 5 {
		r.send(t, r.direct, "bob", "status", "working on it\n")
		r.step(t, 4*time.Minute)
	}
	p, now = r.proof(t)
	assert.Equal(t, bus.PushProven, p.State(now))
	assert.True(t, p.At.After(first), "renewed while up: %s then %s", first, p.At)
	assert.Equal(t, "n1", p.Nonce, "the proof names the round trip it rests on")

	// the daemon stops (no more steps): the proof goes stale at PushFresh
	r.store.Advance(bus.PushFresh)
	p, now = r.proof(t)
	assert.Equal(t, bus.PushStale, p.State(now))
	require.Error(t, heard())

	// its clock runs again (the daemon's and the store's move together live):
	// the renewal is due, and the proof is fresh; the next check goes
	// unanswered: down at once
	r.step(t, PushRenewEvery)
	p, now = r.proof(t)
	assert.Equal(t, bus.PushProven, p.State(now), "a running daemon whose session is up renews at once")
	r.step(t, SessionQuiet)
	r.step(t, SessionBound)
	p, now = r.proof(t)
	assert.Equal(t, bus.PushDown, p.State(now))
	assert.Equal(t, NoSessionAnswer, p.Reason)
	assert.Contains(t, heard().Error(), "("+NoSessionAnswer+")")

	r.send(t, r.direct, "bob", PongSubject, PongLine("n2", 0, 0, 4)+"\n")
	r.step(t, BeatEvery)
	p, now = r.proof(t)
	assert.Equal(t, bus.PushProven, p.State(now), "the next answer proves it again")
	assert.Equal(t, "n2", p.Nonce)

	r.mu.Lock()
	defer r.mu.Unlock()
	assert.Contains(t, r.lines[len(r.lines)-1], "push proof: up: the session answered n2")
}

// TestAPassiveHarnessProvesNoPush: a harness with no deliver command has its
// check put on its own stream, so a session that answers it reads its stream
// by hand: nothing pushes. Its proof is written down, saying so.
func TestAPassiveHarnessProvesNoPush(t *testing.T) {
	t.Parallel()
	r := newProverRig(t, Stub{Harness: "custom"})
	r.step(t, BeatEvery)
	r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 4)+"\n")
	r.step(t, BeatEvery)
	up, _ := r.present(t)
	require.True(t, up, "the session answered on its stream")
	p, now := r.proof(t)
	assert.Equal(t, bus.PushDown, p.State(now), "and still nothing pushes into it")
	assert.Equal(t, passiveReason, p.Reason)
}

// TestAProofThatCannotBeWrittenIsSaidAndTriedAgain: the store failing a
// write is a line in the daemon's log, never a stop, and the next save
// writes it.
func TestAProofThatCannotBeWrittenIsSaidAndTriedAgain(t *testing.T) {
	t.Parallel()
	r := newProverRig(t, nil)
	r.store.Fail = context.DeadlineExceeded
	r.prover.Step(PresenceStatus{Friend: "bob", Presence: PresenceUp, Answers: 1})
	r.store.Fail = nil
	p, now := r.proof(t)
	assert.Equal(t, bus.PushNone, p.State(now))
	r.mu.Lock()
	assert.Contains(t, r.lines[0], "push proof: not written: context deadline exceeded")
	r.mu.Unlock()
	r.prover.Step(PresenceStatus{Friend: "bob", Presence: PresenceUp, Answers: 1})
	p, now = r.proof(t)
	assert.Equal(t, bus.PushProven, p.State(now))
}

// TestAMissingProviderKeyPushProofReadsDownWithReasonNeverProven verifies that
// a friend whose provider key is not in her environment reads down with the
// reason (5480 --needs-env), never as proven (Glenn 2026-10-08).
func TestAMissingProviderKeyPushProofReadsDownWithReasonNeverProven(t *testing.T) {
	t.Parallel()
	r := newProverRig(t, nil)
	r.prover.Harness = "opencode"
	r.prover.NeedsEnv = []string{"INCEPTION_API_KEY"}
	env := map[string]string{}
	r.prover.Getenv = func(k string) string { return env[k] }

	// Daemon starts with missing key
	r.step(t, BeatEvery)
	p, now := r.proof(t)
	assert.Equal(t, bus.PushDown, p.State(now))
	assert.Equal(t, "no key sealed: INCEPTION_API_KEY", p.Reason)

	// Even if a session pong arrives, missing key must keep it down
	r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 4)+"\n")
	r.step(t, BeatEvery)
	p, now = r.proof(t)
	assert.Equal(t, bus.PushDown, p.State(now), "missing key must never be proven")
	assert.Equal(t, "no key sealed: INCEPTION_API_KEY", p.Reason)

	// Once key is sealed in environment, proof comes up
	env["INCEPTION_API_KEY"] = "sealed"
	r.step(t, StatusEvery)
	p, now = r.proof(t)
	assert.Equal(t, bus.PushProven, p.State(now), "key sealed allows proven push proof")
	assert.Equal(t, "", p.Reason)

	// If key is unsealed/removed, proof immediately drops back to down
	delete(env, "INCEPTION_API_KEY")
	r.step(t, StatusEvery)
	p, now = r.proof(t)
	assert.Equal(t, bus.PushDown, p.State(now), "unsealed key drops push proof back to down")
	assert.Equal(t, "no key sealed: INCEPTION_API_KEY", p.Reason)
}

// TestOpenCodeExit125ReadsDownWithNeedsEnvReasonNeverProven verifies that when
// opencode fails delivery verification with exit 125, the push proof reads
// down with the 5480 reason, never as proven.
func TestOpenCodeExit125ReadsDownWithNeedsEnvReasonNeverProven(t *testing.T) {
	t.Parallel()
	r := newProverRig(t, nil)
	r.prover.Harness = "opencode"
	r.prover.NeedsEnv = []string{"INCEPTION_API_KEY"}
	env := map[string]string{"INCEPTION_API_KEY": "present"}
	r.prover.Getenv = func(k string) string { return env[k] }

	var verifyErr error
	r.prover.Verify = func(ctx context.Context) error { return verifyErr }

	// Daemon starts and asks check n1
	r.step(t, BeatEvery)

	// Session answers check, proof is up
	r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 4)+"\n")
	r.step(t, BeatEvery)
	p, now := r.proof(t)
	assert.Equal(t, bus.PushProven, p.State(now))

	// Opencode session list exits 125 during periodic renewal
	verifyErr = fmt.Errorf("opencode session list exited 125")
	r.step(t, PushRenewEvery)
	p, now = r.proof(t)
	assert.Equal(t, bus.PushDown, p.State(now), "opencode exit 125 must read as down, never proven")
	assert.Equal(t, "no key sealed: opencode exit 125", p.Reason)

	// With missing env also detected, it names the exact key
	delete(env, "INCEPTION_API_KEY")
	r.step(t, PushRenewEvery)
	p, now = r.proof(t)
	assert.Equal(t, bus.PushDown, p.State(now))
	assert.Equal(t, "no key sealed: INCEPTION_API_KEY", p.Reason)
}

// TestPushProofRefreshedOnlyWhenDeliveryTakenOrCapabilityVerified verifies that
// push proof renewal only refreshes when delivery capability is verified or
// when the session actually took a delivery.
func TestPushProofRefreshedOnlyWhenDeliveryTakenOrCapabilityVerified(t *testing.T) {
	t.Parallel()
	r := newProverRig(t, nil)
	r.prover.Harness = "opencode"
	var verifyErr error
	verifiedCount := 0
	r.prover.Verify = func(ctx context.Context) error {
		verifiedCount++
		return verifyErr
	}

	// Daemon starts and asks check n1
	r.step(t, BeatEvery)

	// 1. Session answers check: tookDelivery is true, initial proof is written
	r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 4)+"\n")
	r.step(t, BeatEvery)
	p, now := r.proof(t)
	assert.Equal(t, bus.PushProven, p.State(now))
	assert.Equal(t, 0, verifiedCount, "answering a check is taking delivery, no verify needed")

	// 2. Renewal due: verifyDelivery is called
	r.step(t, PushRenewEvery)
	p, now = r.proof(t)
	assert.Equal(t, bus.PushProven, p.State(now))
	assert.Equal(t, 1, verifiedCount, "renewal calls verifyDelivery")

	// 3. Verification fails: renewal fails, proof is written down
	verifyErr = fmt.Errorf("runner failure")
	r.step(t, PushRenewEvery)
	p, now = r.proof(t)
	assert.Equal(t, bus.PushDown, p.State(now))
	assert.Equal(t, "runner failure", p.Reason)
	assert.Equal(t, 2, verifiedCount)

	// 4. Session takes another delivery: next check goes in and is answered
	verifyErr = nil
	r.step(t, ProveEvery)
	r.send(t, r.direct, "bob", PongSubject, PongLine("n2", 0, 0, 4)+"\n")
	r.step(t, BeatEvery)
	p, now = r.proof(t)
	assert.Equal(t, bus.PushProven, p.State(now), "taking new delivery restores proof")
	assert.Equal(t, "n2", p.Nonce)
}

// TestOpenCodeVerifyDeliveryAdapter verifies OpenCode.VerifyDelivery behavior
// against session list exit codes.
func TestOpenCodeVerifyDeliveryAdapter(t *testing.T) {
	t.Parallel()
	oc := &OpenCode{
		Dir:     t.TempDir(),
		Program: "opencode",
		Run: func(ctx context.Context, dir, prog string, args []string, stdin string) (string, int, error) {
			if len(args) >= 2 && args[0] == "session" && args[1] == "list" {
				return "", 125, nil
			}
			return "[]", 0, nil
		},
	}
	err := oc.VerifyDelivery(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "opencode session list exited 125")

	oc.Run = func(ctx context.Context, dir, prog string, args []string, stdin string) (string, int, error) {
		return "[]", 0, nil
	}
	err = oc.VerifyDelivery(context.Background())
	require.NoError(t, err)
}
