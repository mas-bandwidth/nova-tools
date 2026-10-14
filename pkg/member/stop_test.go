package member

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

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

func (r *stopRunner) child(card string) *stopChild {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.children[card]
}

func stopRig(width int, cfg Config) (*Member, *scriptSprint, *stopRunner, *strings.Builder) {
	s, out := newScript(), &strings.Builder{}
	r := &stopRunner{fakeRunner: newRunner(), children: map[string]*stopChild{}}
	return New(cfg, rowWidth(s, width), r, &fakePusher{}, out), s, r, out
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
	m, s, r, out := stopRig(2, Config{As: "m"})
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
	assert.True(t, m.stopped)
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
	assert.False(t, m.stopped)
	assert.Equal(t, 1, m.Running())
	assert.Equal(t, []string{"c1", "c1"}, r.started(), "taken again at the queue's word")
	assert.Contains(t, out.String(), "MEMBER START machine RUNNING: taking cards again")
}

// NoLaunchAfterStop: a queue that says STOPPED starts nothing, however many cards it holds
// ready or working with no child of ours; the word is read every pass before any start.
func TestStoppedTakesAndRecoversNothing(t *testing.T) {
	t.Parallel()
	m, s, r, _ := stopRig(4, Config{As: "m"})
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

// GateBeforeStart, NoLaunchAfterStop (tla/StopCancels.tla): the word is read again right
// before each launch, not only at the head of the pass. A Background member takes two cards
// in one RUNNING pass; the first start to take startMu launches at once, the second blocks
// in Sleep for the whole StartGap (the clock is fixed, so none of the gap has passed). The
// machine is STOPPED while it waits, so when the wait ends the second start must read the
// word STOPPED and launch no child: the runner starts exactly one card, and the other is
// handed back with stop-return, never finished (the stop owns it).
func TestAStartQueuedBehindStartMuDoesNotLaunchAfterStop(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	sleep := func(time.Duration) {
		once.Do(func() { close(entered) })
		<-release
	}
	m, s, r, out := stopRig(2, Config{
		As: "m", Background: true,
		Clock: func() time.Time { return time.Unix(0, 0) },
		Sleep: sleep,
	})
	p1, p2 := pk("c1"), pk("c2")
	s.set("queue", 0, queueWith(t, "RUNNING", 7, ready("c1"), ready("c2")))
	s.set("take", 0, takeJSON(t, p1, p2))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)

	// one start takes startMu and launches at once; the other is in Sleep, the fixed clock
	// leaving the whole StartGap to wait
	<-entered

	// the machine STOPPED while the queued start waits: both cards are working under this row
	s.set("queue", 0, queueWith(t, "STOPPED", 7, working("c1", 1, &p1), working("c2", 1, &p2)))
	_, err = m.Tick(time.Unix(10, 0))
	require.NoError(t, err)
	require.True(t, m.stopped)

	// the wait ends: the queued start runs now, after the stop
	close(release)
	m.WaitLong()

	assert.Len(t, r.started(), 1, "a start queued behind startMu launches no child after STOP: %s", out.String())
	blocked := "c1"
	started := r.started()
	if len(started) > 0 && started[0] == "c1" {
		blocked = "c2"
	}

	// the next pass: the card the stop took has no child of ours, so the stop hands it back
	_, err = m.Tick(time.Unix(20, 0))
	require.NoError(t, err)
	assert.Equal(t, []string{"stop-return --as m " + blocked + "@1 --epoch 7 --reason owned process stopped"}, s.lines("stop-return"))
	assert.Empty(t, s.lines("finish"), "a card the stop took is never finished: %s", out.String())
	assert.NotContains(t, out.String(), "launch refused", "no failed launch for the cancelled start: %s", out.String())
}

// A stop-return the server refuses is said once while its text stands and tried again after
// StopReturnRetry, whatever the word: the machine RUNNING again between the refusal and the
// retry (start by hand, or a store that counted only the friends' owed returns) still hands
// the card back and frees the lane, and the card is never finished meanwhile.
func TestARefusedStopReturnIsTriedAgainWhateverTheWord(t *testing.T) {
	t.Parallel()
	m, s, r, out := stopRig(1, Config{As: "m"})
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

// The beat reads OwedStopReturns on its own goroutine while Tick writes m.running: a child
// is running, then the machine is STOPPED, so machineStop marks the launch stopped, and
// once the child has ended the launch is forgotten. Ranging m.running from that read is a
// concurrent map read and write. The count is an atomic, so the read does not range the map.
func TestTheBeatReadsOwedStopReturnsWhileTickWritesRunning(t *testing.T) {
	t.Parallel()
	m, s, r, _ := stopRig(1, Config{As: "m"})
	p := pk("c1")
	s.set("queue", 0, queueWith(t, "RUNNING", 7, ready("c1")))
	s.set("take", 0, takeJSON(t, p))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			_ = m.OwedStopReturns()
		}
	}()

	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	require.Equal(t, 1, m.Running())

	s.set("queue", 0, queueWith(t, "STOPPED", 7, working("c1", 1, &p)))
	for range 2 {
		_, err = m.Tick(time.Unix(10, 0))
		require.NoError(t, err)
	}
	c := r.child("c1")
	require.NotNil(t, c)
	assert.Equal(t, 1, c.stops(), "machineStop wrote the launch stopped")
	assert.Equal(t, 1, m.OwedStopReturns(), "owed while the stopped launch is still running")

	c.end(Result{OK: true, Head: "abc"})
	_, err = m.Tick(time.Unix(20, 0))
	require.NoError(t, err)
	assert.Equal(t, 0, m.Running())
	assert.Equal(t, 0, m.OwedStopReturns(), "forgotten: nothing owed")

	cancel()
	wg.Wait()
}

// The endEnded guard, red without it: a child the stop cancelled ends with a result after
// the machine runs again; the result is nobody's, the card is handed back, never finished.
func TestACancelledChildEndingUnderRunningIsHandedBackNotFinished(t *testing.T) {
	t.Parallel()
	m, s, r, out := stopRig(1, Config{As: "m"})
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
	m, s, r, out := stopRig(1, Config{As: "m"})
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
	m, s, r, out := stopRig(4, Config{As: "m"})
	p := pk("c2")
	p.Gen = 3
	s.set("queue", 0, queueWith(t, "STOPPED", 7, ready("c1"), working("c2", 3, &p)))
	s.set("take", 0, takeJSON(t, pk("c1")))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	assert.Equal(t, []string{"stop-return --as m c2@3 --epoch 7 --reason owned process stopped"}, s.lines("stop-return"))
	assert.Empty(t, s.lines("take"))
	assert.Empty(t, r.started(), "no recovery launch while STOPPED")
	assert.Contains(t, out.String(), "STOP-RETURN OK card=c2 gen=3 epoch=7 pid=0: handed back to m ready by the machine's stop (no child of this member runs it")
	// c2 was handed back OK: the next pass does not send it again (handedBack check)
	for i := range 3 {
		_, err = m.Tick(time.Unix(1+int64(i), 0))
		require.NoError(t, err)
	}
	assert.Len(t, s.lines("stop-return"), 1, "c2 is not sent again once handed back")

	// a new working card whose stop-return is refused is tried again after StopReturnRetry, not every pass
	p3 := pk("c3")
	s.set("queue", 0, queueWith(t, "STOPPED", 7, ready("c1"), working("c3", 1, &p3)))
	s.set("stop-return", 2, "the machine is RUNNING")
	for i := range 3 {
		_, err = m.Tick(time.Unix(10+int64(i), 0))
		require.NoError(t, err)
	}
	assert.Len(t, s.lines("stop-return"), 2, "c3 sent once, then waits for StopReturnRetry")
	_, err = m.Tick(time.Unix(10, 0).Add(StopReturnRetry + 5*time.Second))
	require.NoError(t, err)
	assert.Len(t, s.lines("stop-return"), 3, "c3 tried again after StopReturnRetry")
}

// A server before the word (no machine field) changes nothing: the member runs as it did.
func TestAQueueWithoutTheMachineWordRunsAsBefore(t *testing.T) {
	t.Parallel()
	m, s, r, _ := stopRig(2, Config{As: "m"})
	s.set("queue", 0, queueJSON(t, 7, ready("c1")))
	s.set("take", 0, takeJSON(t, pk("c1")))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	assert.False(t, m.stopped)
	assert.Equal(t, []string{"c1"}, r.started())
}

// A child that is no Stopper is left to end by itself and handed back when it has.
func TestAChildThatCannotBeStoppedIsHandedBackWhenItEnds(t *testing.T) {
	t.Parallel()
	g := newWidthRig(1, Config{As: "m"})
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

// Beat trace: the member sends --stop-returns on every beat, 0 included, so the store's
// owed count clears to 0 after the last hand-back and start does not wait for ever.
func TestBeatAlwaysSendsStopReturnsFlagIncludingZero(t *testing.T) {
	t.Parallel()
	m, s, r, _ := stopRig(2, Config{As: "m"})
	p := pk("c1")
	s.set("queue", 0, queueWith(t, "RUNNING", 7, ready("c1")))
	s.set("take", 0, takeJSON(t, p))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)

	// initial beat with 0 owed
	s.reset()
	require.NoError(t, m.Beat())
	assert.Equal(t, []string{"fleet beat m --stop-returns 0 --load 0.0"}, s.lines("beat"))

	// the machine stops: c1 is cancelled, now 1 stop-return is owed
	s.set("queue", 0, queueWith(t, "STOPPED", 7, working("c1", 1, &p)))
	_, err = m.Tick(time.Unix(10, 0))
	require.NoError(t, err)
	s.reset()
	require.NoError(t, m.Beat())
	assert.Equal(t, []string{"fleet beat m --stop-returns 1 --load 0.0"}, s.lines("beat"))

	// child ends and is handed back via stop-return
	r.child("c1").end(Result{OK: true})
	_, err = m.Tick(time.Unix(20, 0))
	require.NoError(t, err)
	assert.Equal(t, []string{"stop-return --as m c1@1 --epoch 7 --reason owned process stopped"}, s.lines("stop-return"))

	// beat after hand-back: owed returns to 0
	s.reset()
	require.NoError(t, m.Beat())
	assert.Equal(t, []string{"fleet beat m --stop-returns 0 --load 0.0"}, s.lines("beat"))
}

// While Room says no (the disk floor), the member's fleet beat carries its word (--no-room),
// so the deal gives it no card; the first tick Room says yes, the beat carries none.
func TestBeatCarriesNoRoomWhileRoomSaysNo(t *testing.T) {
	t.Parallel()
	room, why := false, "free disk 0.0 GiB under the floor of 10 GiB"
	m, s, _, _ := stopRig(2, Config{As: "m", Room: func() (bool, string) { return room, why }})
	s.set("queue", 0, queueWith(t, "RUNNING", 7))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	s.reset()
	require.NoError(t, m.Beat())
	assert.Equal(t, []string{"fleet beat m --stop-returns 0 --no-room " + why + " --load 0.0"}, s.lines("beat"))
	room = true
	_, err = m.Tick(time.Unix(10, 0))
	require.NoError(t, err)
	s.reset()
	require.NoError(t, m.Beat())
	assert.Equal(t, []string{"fleet beat m --stop-returns 0 --load 0.0"}, s.lines("beat"))
}

// A draining member's beat carries this tick's word, not a stale one: Room is asked before
// the drain return, so a member that drained under its floor says room again once the disk is
// freed, and says no room again when it falls back under the floor (v1.2.6, nova-tools#5569
// follow-up). Reversed witness: with room asked only past the drain return, the second beat
// still carries the stale word (checked on hetzner2: the test fails there without the fix).
func TestADrainingMembersBeatCarriesThisTicksNoRoom(t *testing.T) {
	t.Parallel()
	room, why := false, "free disk 0.0 GiB under the floor of 10 GiB"
	m, s, _, _ := stopRig(2, Config{As: "m", Room: func() (bool, string) { return room, why }})
	s.set("queue", 0, queueWith(t, "RUNNING", 7))
	beat := func(at int64) []string {
		t.Helper()
		_, err := m.Tick(time.Unix(at, 0))
		require.NoError(t, err)
		s.reset()
		require.NoError(t, m.Beat())
		return s.lines("beat")
	}
	assert.Equal(t, []string{"fleet beat m --stop-returns 0 --no-room " + why + " --load 0.0"}, beat(0))
	m.Drain()
	room = true
	assert.Equal(t, []string{"fleet beat m --stop-returns 0 --draining --load 0.0"}, beat(10), "the disk freed while draining: the word is gone, the drain is said")
	room = false
	assert.Equal(t, []string{"fleet beat m --stop-returns 0 --no-room " + why + " --draining --load 0.0"}, beat(20), "under the floor again while draining: the word is said")
}

// A member that drains (its binary was replaced) says so on every beat from the drain on
// (--draining), so the server holds the state: the deal gives it nothing and returns what it
// was dealt and has not taken (nova-sprint tla/NoRoom.tla, NeverDealtToDraining; the owner,
// 2026-10-11: "Why are cards stuck in ready."). Before the drain its beat says nothing of it.
func TestADrainingMembersBeatSaysItDrains(t *testing.T) {
	t.Parallel()
	m, s, _, _ := stopRig(2, Config{As: "m"})
	s.set("queue", 0, queueWith(t, "RUNNING", 7))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	s.reset()
	require.NoError(t, m.Beat())
	assert.Equal(t, []string{"fleet beat m --stop-returns 0 --load 0.0"}, s.lines("beat"), "a member that takes says nothing of a drain")
	m.Drain()
	for at := int64(10); at <= 20; at += 10 {
		_, err = m.Tick(time.Unix(at, 0))
		require.NoError(t, err)
		s.reset()
		require.NoError(t, m.Beat())
		assert.Equal(t, []string{"fleet beat m --stop-returns 0 --draining --load 0.0"}, s.lines("beat"), "every beat from the drain on says it")
	}
}

// A reader member restarted mid-stop: every reading card under its row with no child of ours
// is handed back at its claim generation (not attempt), card@0 is guarded and never emitted,
// and the cards are not recovered when RUNNING again.
func TestAReaderMemberRestartedMidStopHandsBackItsReadingCards(t *testing.T) {
	t.Parallel()
	m, s, r, out := stopRig(4, Config{As: "reader.r1", Reader: true})
	p1 := pk("rd1")
	p1.Gen = 1
	p1.Attempt = 3 // attempt is 3, but generation is 1!
	pZero := pk("rd-zero")
	pZero.Gen = 0 // unpopulated gen: must never emit card@0

	s.set("queue", 0, queueWith(t, "STOPPED", 7, queueCard{ID: "rd1", Col: "reading", Packet: &p1}, queueCard{ID: "rd-zero", Col: "reading", Packet: &pZero}))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)

	// rd1 handed back at generation 1 (not attempt 3!), rd-zero never emitted
	assert.Equal(t, []string{"stop-return --as reader.r1 rd1@1 --epoch 7 --reason owned process stopped"}, s.lines("stop-return"))
	assert.NotContains(t, strings.Join(s.lines("stop-return"), " "), "rd-zero@0")
	assert.Contains(t, out.String(), "STOP-RETURN OK card=rd1 gen=1 epoch=7 pid=0: handed back to reader.r1 ready by the machine's stop")

	// subsequent ticks while STOPPED do not duplicate the stop-return
	for i := range 3 {
		_, err = m.Tick(time.Unix(1+int64(i), 0))
		require.NoError(t, err)
	}
	assert.Len(t, s.lines("stop-return"), 1, "rd1 is not sent again once handed back")

	// RUNNING again: recoverWorking checks gen == g, so it is not recovered at this generation
	s.set("queue", 0, queueWith(t, "RUNNING", 7, queueCard{ID: "rd1", Col: "reading", Packet: &p1}))
	_, err = m.Tick(time.Unix(10, 0))
	require.NoError(t, err)
	assert.Empty(t, r.started(), "handed back reading card is not recovered at the same generation")
}
