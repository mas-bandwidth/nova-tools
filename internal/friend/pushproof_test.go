package friend

import (
	"context"
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
