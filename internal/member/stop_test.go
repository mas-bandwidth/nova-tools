package member

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The machine's stop cancels jobs (machineStop; tla/StopCancels.tla). These tests are the
// model's traces: a lane under way when the queue says STOPPED is told to stop and, once
// ended, handed back with stop-return and never finished (EveryLaneReturnsOnStop); nothing is
// taken or recovered while STOPPED, whatever the queue holds (NoLaunchAfterStop,
// NoLaneWhileStopped); a refused stop-return is tried again; RUNNING again takes the queue's
// cards at the generation it carries.

// stopChild is a fakeChild that is a Stopper: Stop records that it was told to stop and says
// its pid; the test ends it by hand, as the runner's own grace would.
type stopChild struct {
	fakeChild
	mu      sync.Mutex
	stopped int
	pid     int
}

func TestStopCancelsBlockedScriptVerifierBeforeModelFallback(t *testing.T) {
	t.Parallel()
	entered, cancelled := make(chan struct{}), make(chan struct{})
	m, s, r, _ := stopRig(Config{As: "r", Width: 1, Reader: true, Background: true,
		ScriptVerify: func(ctx context.Context, _ Packet, _ cardhdr.Class) (bool, string) {
			close(entered)
			<-ctx.Done()
			close(cancelled)
			return false, "cancelled"
		},
	})
	p := pk("r1")
	p.Kind, p.Brief = "read", scriptBrief
	require.True(t, m.start(p))
	<-entered
	done := make(chan struct{})
	go func() {
		m.machineStop(queueOut{Machine: "STOPPED", Epoch: p.Epoch}, map[string]queueCard{"r1": reading("r1", &p)}, time.Now())
		close(done)
	}()
	<-done // a verifier waiting for cancellation must not hold the STOP mutex
	<-cancelled
	m.WaitLong()
	m.machineStop(queueOut{Machine: "STOPPED", Epoch: p.Epoch}, map[string]queueCard{"r1": reading("r1", &p)}, time.Now())
	assert.Empty(t, r.started(), "STOP cannot allow the verifier's model fallback")
	assert.Empty(t, s.lines("report"), "a cancelled script read must not report a verdict")
}

func TestStopStartCannotRevivePreStopScriptFallback(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	m, _, r, _ := stopRig(Config{As: "r", Width: 1, Reader: true, Background: true,
		ScriptVerify: func(context.Context, Packet, cardhdr.Class) (bool, string) {
			close(entered)
			<-release // a verifier that ignores cancellation must still lose admission
			return false, "no verdict"
		},
	})
	p := pk("r1")
	p.Kind, p.Brief = "read", scriptBrief
	require.True(t, m.start(p))
	<-entered
	m.machineStop(queueOut{Machine: "STOPPED", Epoch: p.Epoch}, map[string]queueCard{"r1": reading("r1", &p)}, time.Now())
	m.machineStop(queueOut{Machine: "RUNNING", Epoch: p.Epoch}, map[string]queueCard{"r1": reading("r1", &p)}, time.Now())
	close(release)
	m.WaitLong()
	m.collect()
	assert.Empty(t, r.started(), "a pre-STOP script read cannot launch after START")
}

func TestScriptFallbackRereadsAdmissionImmediatelyBeforeModelStart(t *testing.T) {
	t.Parallel()
	var admits atomic.Int32
	m, _, r, _ := stopRig(Config{As: "r", Width: 1, Reader: true,
		Admit: func(Packet) error {
			if admits.Add(1) == 2 {
				return fmt.Errorf("claim moved during script verification")
			}
			return nil
		},
		ScriptVerify: func(context.Context, Packet, cardhdr.Class) (bool, string) { return false, "no verdict" },
	})
	p := pk("r1")
	p.Kind, p.Brief = "read", scriptBrief
	m.start(p)
	assert.Equal(t, int32(2), admits.Load())
	assert.Empty(t, r.started(), "a script fallback must use a fresh claim admission")
}

func TestOldLaunchCleanupPreservesNewGenerationVerifierCancellation(t *testing.T) {
	t.Parallel()
	old := &verifyCancel{cancel: func() {}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	newEntry := &verifyCancel{cancel: cancel}
	m := &Member{out: io.Discard, verifyCancels: map[string]*verifyCancel{"r1": newEntry}}
	m.clearVerifyCancel("r1", old)
	require.Same(t, newEntry, m.verifyCancels["r1"])
	m.machineStop(queueOut{Machine: "STOPPED"}, nil, time.Now())
	assert.ErrorIs(t, ctx.Err(), context.Canceled, "STOP must still cancel the new generation")
}

type proofStopChild struct {
	stopChild
	confirmed bool
}

func (c *proofStopChild) StopConfirmed() bool { return c.confirmed }

func (c *stopChild) Stop() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped++
	return c.pid
}

func (c *stopChild) stops() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopped
}

// stopRunner hands out stopChild children.
type stopRunner struct {
	*fakeRunner
	mu       sync.Mutex
	children map[string]*stopChild
}

func (r *stopRunner) Start(p Packet) (Child, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fakeRunner.mu.Lock()
	r.fakeRunner.packets = append(r.fakeRunner.packets, p)
	r.fakeRunner.mu.Unlock()
	c := &stopChild{pid: 4000 + len(r.children)}
	r.children[p.Card] = c
	return c, nil
}

func (r *stopRunner) RecoverStopped(Packet) Child {
	c := &stopChild{}
	c.end(Result{}) // the fake's old run has ended before recovery
	return c
}

func (r *stopRunner) child(card string) *stopChild {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.children[card]
}

func stopRig(cfg Config) (*Member, *scriptSprint, *stopRunner, *strings.Builder) {
	s, out := newScript(), &strings.Builder{}
	r := &stopRunner{fakeRunner: newRunner(), children: map[string]*stopChild{}}
	return New(cfg, s, r, &fakePusher{}, out), s, r, out
}

func queueWith(t *testing.T, machine string, epoch uint64, cards ...queueCard) string {
	t.Helper()
	b, err := json.Marshal(queueOut{As: "m", Epoch: epoch, Cards: cards, Machine: machine})
	require.NoError(t, err)
	return string(b)
}

// Trace: take, run, STOPPED, child told to stop, child ends, stop-return, forgotten; RUNNING
// again, the card taken at the queue's new generation.
func TestStoppedCancelsTheLaneAndHandsTheCardBackWithStopReturn(t *testing.T) {
	t.Parallel()
	m, s, r, out := stopRig(Config{As: "m", Width: 2})
	p := pk("c1")
	s.set("queue", 0, queueWith(t, "RUNNING", 7, ready("c1")))
	s.set("take", 0, takeJSON(t, p))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	require.Equal(t, 1, m.Running())
	c := r.child("c1")
	require.NotNil(t, c)

	// the machine stops while the child runs: it is told to stop, once, and nothing is reported
	s.set("queue", 0, queueWith(t, "STOPPED", 7, working("c1", 1, &p)))
	for range 2 {
		_, err = m.Tick(time.Unix(10, 0))
		require.NoError(t, err)
	}
	assert.Equal(t, 1, c.stops(), "the child is told to stop once")
	assert.True(t, m.Stopped())
	assert.Empty(t, s.lines("stop-return"), "no stop-return before the child has ended")
	assert.Empty(t, s.lines("finish"), "nothing is finished under the stop")
	assert.Contains(t, out.String(), "MEMBER STOP machine STOPPED: taking no card; 1 running lane(s) are cancelled")
	assert.Contains(t, out.String(), "LANE CANCELLED BY STOP card=c1 gen=1 epoch=7 pid=4000")

	// the child ends (with a result: it is never finished, the stop owns it)
	c.end(Result{OK: true, Head: "abc"})
	_, err = m.Tick(time.Unix(20, 0))
	require.NoError(t, err)
	assert.Equal(t, []string{"stop-return --as m c1@1 --epoch 7 --reason owned process stopped"}, s.lines("stop-return"))
	assert.Empty(t, s.lines("finish"), "a cancelled card is never finished (EveryLaneReturnsOnStop)")
	assert.Equal(t, 0, m.Running(), "the lane is free once the card is handed back")
	assert.Equal(t, []string{"c1:true"}, r.endedLaunches(), "the launch is ended for the runner, its tree kept (failed: a person may inspect it)")
	assert.Contains(t, out.String(), "STOP-RETURN OK card=c1 gen=1 epoch=7 pid=4000")

	// RUNNING again: the queue carries the card at its new generation and it is taken as any
	p2 := p
	p2.Gen = 2
	s.set("queue", 0, queueWith(t, "RUNNING", 7, ready("c1")))
	s.set("take", 0, takeJSON(t, p2))
	_, err = m.Tick(time.Unix(30, 0))
	require.NoError(t, err)
	assert.False(t, m.Stopped())
	assert.Equal(t, 1, m.Running())
	assert.Equal(t, []string{"c1", "c1"}, r.started(), "taken again at the queue's word")
	assert.Contains(t, out.String(), "MEMBER START machine RUNNING: taking cards again")
}

// NoLaunchAfterStop: a queue that says STOPPED starts nothing, however many cards it holds
// ready or working with no child of ours; the word is read every pass before any start.
func TestStoppedTakesAndRecoversNothing(t *testing.T) {
	t.Parallel()
	m, s, r, _ := stopRig(Config{As: "m", Width: 4})
	p := pk("c2")
	s.set("queue", 0, queueWith(t, "STOPPED", 7, ready("c1"), working("c2", 1, &p)))
	s.set("take", 0, takeJSON(t, pk("c1")))
	for range 3 {
		_, err := m.Tick(time.Unix(0, 0))
		require.NoError(t, err)
	}
	assert.Empty(t, s.lines("take"), "no take while STOPPED")
	assert.Empty(t, r.started(), "no recovery while STOPPED")
	assert.Equal(t, 0, m.Running())
}

func TestStoppedParentExitKeepsReturnOwedUntilGroupProof(t *testing.T) {
	t.Parallel()
	m, s, _, _ := stopRig(Config{As: "m", Width: 1})
	p := pk("c1")
	c := &proofStopChild{}
	c.end(Result{}) // parent exit alone is insufficient
	m.running["c1"] = launch{child: c, gen: 1, epoch: 7, packet: p, stopped: true}
	s.set("queue", 0, queueWith(t, "STOPPED", 7, working("c1", 1, &p)))
	_, err := m.Tick(time.Unix(10, 0))
	require.NoError(t, err)
	assert.Empty(t, s.lines("stop-return"))
	assert.Equal(t, 1, m.OwedStopReturns())
	c.confirmed = true
	_, err = m.Tick(time.Unix(11, 0))
	require.NoError(t, err)
	assert.Len(t, s.lines("stop-return"), 1)
}

func TestStopWaitsForInFlightStartAndCancelsItsChild(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	release := make(chan struct{})
	m, _, r, _ := stopRig(Config{As: "m", Width: 1, Background: true, Admit: func(Packet) error {
		close(entered)
		<-release
		return nil
	}})
	p := pk("c1")
	require.True(t, m.start(p))
	<-entered
	done := make(chan struct{})
	go func() {
		m.machineStop(queueOut{Machine: "STOPPED", Epoch: p.Epoch}, map[string]queueCard{"c1": working("c1", p.Gen, &p)}, time.Now())
		close(done)
	}()
	close(release)
	<-done // the package test timeout detects a broken start/STOP handshake
	m.WaitLong()
	require.NotNil(t, r.child("c1"))
	assert.Equal(t, 1, r.child("c1").stops())
	assert.Equal(t, 1, m.OwedStopReturns())
}

func TestAdmissionRefusalDuringStopReturnsWithoutStarting(t *testing.T) {
	t.Parallel()
	m, s, r, _ := stopRig(Config{As: "m", Width: 1, Admit: func(Packet) error { return fmt.Errorf("fresh queue says STOPPED") }})
	p := pk("c1")
	m.start(p)
	assert.Empty(t, r.started())
	s.set("stop-return", 0, "ok")
	m.machineStop(queueOut{Machine: "STOPPED", Epoch: p.Epoch}, map[string]queueCard{"c1": working("c1", p.Gen, &p)}, time.Now())
	assert.Len(t, s.lines("stop-return"), 1)
	assert.Equal(t, 0, m.OwedStopReturns())
}

func TestReturnedReadBeginsAtQueuedGeneration(t *testing.T) {
	t.Parallel()
	m, s, _, _ := stopRig(Config{As: "r", Width: 1, Reader: true})
	p := pk("r1")
	p.Kind, p.Attempt, p.Gen = "read", 1, 0 // old packet omits the new generation
	c := asked("r1", &p)
	c.Gen = 2 // stop-return raised the stored generation
	s.set("queue", 0, queueWith(t, "RUNNING", 7, c))
	_, err := m.Tick(time.Unix(10, 0))
	require.NoError(t, err)
	assert.Equal(t, []string{"read --as r --begin r1@2 --epoch 7"}, s.lines("begin"))
	assert.Equal(t, 2, m.running["r1"].gen)
}

// A stop-return the server refuses is said once while its text stands and tried again after
// StopReturnRetry, whatever the word: the machine RUNNING again between the refusal and the
// retry (start by hand, or a store that counted only the friends' owed returns) still hands
// the card back and frees the lane, and the card is never finished meanwhile.
func TestARefusedStopReturnIsTriedAgainWhateverTheWord(t *testing.T) {
	t.Parallel()
	m, s, r, out := stopRig(Config{As: "m", Width: 1})
	p := pk("c1")
	s.set("queue", 0, queueWith(t, "RUNNING", 7, ready("c1")))
	s.set("take", 0, takeJSON(t, p))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	c := r.child("c1")
	s.set("queue", 0, queueWith(t, "STOPPED", 7, working("c1", 1, &p)))
	s.set("stop-return", 2, "nova-sprint stop-return: unknown verb")
	c.end(Result{OK: true, Head: "abc"})
	for i := range 3 {
		_, err = m.Tick(time.Unix(10+int64(i), 0))
		require.NoError(t, err)
	}
	assert.Len(t, s.lines("stop-return"), 1, "one try, then StopReturnRetry: no verb a pass")
	assert.Equal(t, 1, strings.Count(out.String(), "STOP-RETURN refused card=c1 gen=1 pid=4000 exit=2: nova-sprint stop-return: unknown verb; tried again in 1m0s"), "the refusal is said once while its text stands: %s", out.String())
	assert.Equal(t, 1, m.Running(), "the lane is held until the server takes the return")
	assert.Equal(t, 1, m.OwedStopReturns(), "owed: the beat carries it")

	// RUNNING again, the card still working at our claim: the return still goes
	s.set("queue", 0, queueWith(t, "RUNNING", 7, working("c1", 1, &p)))
	s.set("take", 0, takeJSON(t))
	_, err = m.Tick(time.Unix(30, 0))
	require.NoError(t, err)
	assert.Len(t, s.lines("stop-return"), 1, "not yet: the retry waits out StopReturnRetry")
	assert.Empty(t, s.lines("finish"), "a cancelled card is never finished, RUNNING or not (the endEnded guard)")
	s.set("stop-return", 0, "STOP-RETURN OK c1@1 -> ready gen=2")
	_, err = m.Tick(time.Unix(0, 0).Add(StopReturnRetry + 11*time.Second))
	require.NoError(t, err)
	assert.Len(t, s.lines("stop-return"), 2, "tried again after StopReturnRetry, RUNNING")
	assert.Equal(t, 0, m.Running(), "the lane frees once the server takes the return")
	assert.Equal(t, 0, m.OwedStopReturns())
	assert.Empty(t, s.lines("finish"))
	assert.Contains(t, out.String(), "STOP-RETURN OK card=c1 gen=1 epoch=7 pid=4000")
}

// The endEnded guard, red without it: a child the stop cancelled ends with a result after
// the machine runs again; the result is nobody's, the card is handed back, never finished.
func TestACancelledChildEndingUnderRunningIsHandedBackNotFinished(t *testing.T) {
	t.Parallel()
	m, s, r, out := stopRig(Config{As: "m", Width: 1})
	p := pk("c1")
	s.set("queue", 0, queueWith(t, "RUNNING", 7, ready("c1")))
	s.set("take", 0, takeJSON(t, p))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	c := r.child("c1")
	s.set("queue", 0, queueWith(t, "STOPPED", 7, working("c1", 1, &p)))
	_, err = m.Tick(time.Unix(10, 0))
	require.NoError(t, err)
	require.Equal(t, 1, c.stops())
	// RUNNING again while the child still runs; then it ends with a good result
	s.set("queue", 0, queueWith(t, "RUNNING", 7, working("c1", 1, &p)))
	s.set("take", 0, takeJSON(t))
	_, err = m.Tick(time.Unix(20, 0))
	require.NoError(t, err)
	c.end(Result{OK: true, Head: "abc"})
	for range 2 {
		_, err = m.Tick(time.Unix(30, 0))
		require.NoError(t, err)
	}
	assert.Empty(t, s.lines("finish"), "the cancelled run's result is nobody's: %s", out.String())
	assert.Equal(t, []string{"stop-return --as m c1@1 --epoch 7 --reason owned process stopped"}, s.lines("stop-return"))
	assert.Equal(t, 0, m.Running())
}

// RUNNING again and the claim moved (the store dealt the card at a new generation after
// its own return, or dropped it): nothing is left to return, the launch is reaped.
func TestACancelledLaunchWhoseClaimMovedIsReaped(t *testing.T) {
	t.Parallel()
	m, s, r, out := stopRig(Config{As: "m", Width: 1})
	p := pk("c1")
	s.set("queue", 0, queueWith(t, "RUNNING", 7, ready("c1")))
	s.set("take", 0, takeJSON(t, p))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	s.set("queue", 0, queueWith(t, "STOPPED", 7, working("c1", 1, &p)))
	_, err = m.Tick(time.Unix(10, 0))
	require.NoError(t, err)
	r.child("c1").end(Result{OK: false})
	p2 := p
	p2.Gen = 2
	s.set("queue", 0, queueWith(t, "RUNNING", 7, working("c1", 2, &p2)))
	s.set("take", 0, takeJSON(t))
	_, err = m.Tick(time.Unix(20, 0))
	require.NoError(t, err)
	assert.Empty(t, s.lines("stop-return"))
	assert.Empty(t, s.lines("finish"))
	assert.Equal(t, 0, m.Running())
	assert.Contains(t, out.String(), "c1: cancelled by stop, and the claim moved under it (gen 1 epoch 7); nothing to return")
}

// A member restarted mid-stop (its old children gone with it): every queue card working
// under its row with no child of ours is handed back at once, and nothing is recovered.
func TestAMemberRestartedMidStopHandsBackItsWorkingCards(t *testing.T) {
	t.Parallel()
	m, s, r, out := stopRig(Config{As: "m", Width: 4})
	p := pk("c2")
	p.Gen = 3
	s.set("queue", 0, queueWith(t, "STOPPED", 7, ready("c1"), working("c2", 3, &p)))
	s.set("take", 0, takeJSON(t, pk("c1")))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	assert.Equal(t, []string{"stop-return --as m c2@3 --epoch 7 --reason owned process stopped"}, s.lines("stop-return"))
	assert.Empty(t, s.lines("take"))
	assert.Empty(t, r.started(), "no recovery launch while STOPPED")
	assert.Contains(t, out.String(), "STOP-RETURN OK card=c2 gen=3 epoch=7 pid=0: handed back to m ready by the machine's stop")
	// a refusal is tried again after StopReturnRetry, not every pass
	s.set("stop-return", 2, "the machine is RUNNING")
	for i := range 3 {
		_, err = m.Tick(time.Unix(1+int64(i), 0))
		require.NoError(t, err)
	}
	assert.Len(t, s.lines("stop-return"), 2)
	_, err = m.Tick(time.Unix(0, 0).Add(StopReturnRetry + 5*time.Second))
	require.NoError(t, err)
	assert.Len(t, s.lines("stop-return"), 3)
}

// A server before the word (no machine field) changes nothing: the member runs as it did.
func TestAQueueWithoutTheMachineWordRunsAsBefore(t *testing.T) {
	t.Parallel()
	m, s, r, _ := stopRig(Config{As: "m", Width: 2})
	s.set("queue", 0, queueJSON(t, 7, ready("c1")))
	s.set("take", 0, takeJSON(t, pk("c1")))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	assert.False(t, m.Stopped())
	assert.Equal(t, []string{"c1"}, r.started())
}

// A child that is no Stopper is left to end by itself and handed back when it has.
func TestAChildThatCannotBeStoppedIsHandedBackWhenItEnds(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 1})
	p := pk("c1")
	g.s.set("queue", 0, queueWith(t, "RUNNING", 7, ready("c1")))
	g.s.set("take", 0, takeJSON(t, p))
	_, err := g.tick(t)
	require.NoError(t, err)
	g.s.set("queue", 0, queueWith(t, "STOPPED", 7, working("c1", 1, &p)))
	_, err = g.tick(t)
	require.NoError(t, err)
	assert.Contains(t, g.out.String(), "LANE CANCELLED BY STOP card=c1 gen=1 epoch=7 pid=0")
	assert.Empty(t, g.s.lines("stop-return"))
	g.r.child("c1").end(Result{OK: true, Head: "abc"})
	_, err = g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, []string{"stop-return --as m c1@1 --epoch 7 --reason owned process stopped"}, g.s.lines("stop-return"))
	assert.Empty(t, g.s.lines("finish"))
}
