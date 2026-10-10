package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
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
// daemon that started proves nothing (down, unheard); the answer writes the
// proof up with the nonce and the harness; while the session stays up the
// daemon renews it every PushRenewEvery, so it never reads stale; a check
// unanswered within its bound writes it down at once; and a daemon that
// stops renewing leaves a proof that goes stale at bus.PushFresh.
func TestTheSessionChecksPongIsThePushProof(t *testing.T) {
	t.Parallel()
	r := newProverRig(t, nil)
	ctx := context.Background()
	heard := func() error { // the advisory line as one error, "" as nil: what nova-bus says beside a message
		lines, err := (&bus.Bus{Store: r.store}).Unheard(ctx, "bob")
		require.NoError(t, err)
		if len(lines) == 0 {
			return nil
		}
		return errors.New(lines[0])
	}

	p, now := r.proof(t)
	assert.Equal(t, bus.PushNone, p.State(now), "no daemon has written one")
	r.step(t, BeatEvery)
	p, now = r.proof(t)
	assert.Equal(t, bus.PushDown, p.State(now), "a daemon that started proves nothing about the session")
	assert.Equal(t, NotYetAnswered, p.Reason)
	require.Error(t, heard())
	assert.Contains(t, heard().Error(), "push=down for bob: no proven push since")

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

// TestAClaudeFriendsCheckGoesInByTheFolder (the owner, 2026-10-08: "why not,
// can we fix the harness to do this?"): a claude friend's daemon has no
// session to put a turn into, so its SESSION CHECK is written as one file,
// <dir>/inbox/SESSION-CHECK-<nonce>, and the beat goes out regardless
// (BeatAlways: her cards' finishes are her presence at the server). A live
// session that watches the folder answers with the pong the file names; the
// pong on the bus brings the presence up, the proof on bus2:push follows with
// the nonce and the harness, and the next beat carries the pong. The next
// check replaces the file; one unanswered within the bound puts her down
// with the check's nonce, the beat still goes out, and nova-bus delivers to
// her all the same, before, during and after.
func TestAClaudeFriendsCheckGoesInByTheFolder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := newProverRig(t, &FolderCheck{Friend: "bob", Dir: dir})
	r.prover.Harness = "claude"
	r.beat = r.sc.BeatAlways(func(context.Context) error { r.beats++; return nil })
	ctx := context.Background()
	deliver := func(subject string) {
		t.Helper()
		_, err := r.direct.Send(ctx, bus.Message{From: "ada", To: []string{"bob"}, Subject: subject, Body: "x\n"})
		require.NoError(t, err, "nova-bus never refuses her on the proof")
	}
	checks := func() []string {
		t.Helper()
		got, err := filepath.Glob(filepath.Join(dir, "inbox", SessionCheckFilePrefix+"*"))
		require.NoError(t, err)
		return got
	}

	// the first beat: the check is in the folder, the beat went out, nothing is proven yet
	deliver("before any check")
	r.step(t, BeatEvery)
	require.Equal(t, []string{SessionCheckFile(dir, "n1")}, checks())
	text, err := os.ReadFile(SessionCheckFile(dir, "n1"))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(text), SessionCheckPrefix+"n1\n"), string(text))
	assert.Contains(t, string(text), "nova-friend pong --as bob --nonce n1")
	assert.Equal(t, 1, r.beats, "the beat is never held back on a claude friend")
	assert.Equal(t, BeatWords{Check: "n1"}, r.sc.Words(), "the beat says the check asked")
	up, why := r.present(t)
	assert.False(t, up)
	assert.Equal(t, NotYetAnswered, why)
	p, now := r.proof(t)
	assert.Equal(t, bus.PushDown, p.State(now))
	lines, err := r.direct.Unheard(ctx, "bob")
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "push=down for bob")
	deliver("while the check stands")

	// a live session watching the folder answers the file: up, proven, and the beat carries it
	r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 4)+"\n")
	r.step(t, BeatEvery)
	up, _ = r.present(t)
	assert.True(t, up, "the session's pong brings her up")
	p, now = r.proof(t)
	assert.Equal(t, bus.PushProven, p.State(now))
	assert.Equal(t, "n1", p.Nonce)
	assert.Equal(t, "claude", p.Harness)
	assert.Equal(t, BeatWords{Check: "n1", Pong: "n1"}, r.sc.Words(), "the beat carries the answer")
	r.sc.Said(r.sc.Words())
	assert.Equal(t, BeatWords{}, r.sc.Words())
	lines, err = r.direct.Unheard(ctx, "bob")
	require.NoError(t, err)
	assert.Empty(t, lines)
	assert.Equal(t, 2, r.beats)

	// silence: the next check replaces the file; unanswered within the bound she is down
	// with its nonce, the beat still goes out, and a message still lands
	r.step(t, SessionQuiet)
	require.Equal(t, []string{SessionCheckFile(dir, "n2")}, checks(), "one check at a time")
	r.step(t, SessionBound)
	up, why = r.present(t)
	assert.False(t, up)
	assert.Equal(t, NoSessionAnswer, why)
	_, reason := r.sc.downBeat(r.now)
	assert.Contains(t, reason, "session check n2")
	p, now = r.proof(t)
	assert.Equal(t, bus.PushDown, p.State(now))
	assert.Equal(t, 4, r.beats, "down or up, every beat went out")
	deliver("after the bound")
	assert.Equal(t, 3, r.store.Len(bus.StreamOf("bob")), "every message to her landed")

	// a text that is no session check is refused by the adapter, nothing written
	exit, err := (&FolderCheck{Friend: "bob", Dir: dir}).Deliver(ctx, "hello\n")
	assert.Equal(t, 1, exit)
	assert.ErrorContains(t, err, "takes only a session check")
	require.Equal(t, []string{SessionCheckFile(dir, "n2")}, checks())
	assert.Contains(t, FolderCheckLine("bob", dir, "/s"), SessionCheckFile(dir, "<nonce>"))
	assert.Contains(t, FolderCheckLine("bob", dir, "/s"), "nova-friend pong --as bob --nonce <nonce> --state-dir /s")
}
