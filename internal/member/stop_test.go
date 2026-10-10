package member

import (
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
	m, s, r, _ := stopRig(Config{As: "m", Width: 2})
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

// Beat trace: the member sends --stop-returns on every beat, 0 included, so the store's
// owed count clears to 0 after the last hand-back and start does not wait for ever.
func TestBeatAlwaysSendsStopReturnsFlagIncludingZero(t *testing.T) {
	t.Parallel()
	m, s, r, _ := stopRig(Config{As: "m", Width: 2})
	p := pk("c1")
	s.set("queue", 0, queueWith(t, "RUNNING", 7, ready("c1")))
	s.set("take", 0, takeJSON(t, p))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)

	// initial beat with 0 owed
	s.reset()
	require.NoError(t, m.Beat())
	assert.Equal(t, []string{"fleet beat m --stop-returns 0"}, beatArgs(s.lines("beat")))

	// the machine stops: c1 is cancelled, now 1 stop-return is owed
	s.set("queue", 0, queueWith(t, "STOPPED", 7, working("c1", 1, &p)))
	_, err = m.Tick(time.Unix(10, 0))
	require.NoError(t, err)
	s.reset()
	require.NoError(t, m.Beat())
	assert.Equal(t, []string{"fleet beat m --stop-returns 1"}, beatArgs(s.lines("beat")))

	// child ends and is handed back via stop-return
	r.child("c1").end(Result{OK: true})
	_, err = m.Tick(time.Unix(20, 0))
	require.NoError(t, err)
	assert.Equal(t, []string{"stop-return --as m c1@1 --epoch 7 --reason owned process stopped"}, s.lines("stop-return"))

	// beat after hand-back: owed returns to 0
	s.reset()
	require.NoError(t, m.Beat())
	assert.Equal(t, []string{"fleet beat m --stop-returns 0"}, beatArgs(s.lines("beat")))
}

// A reader member restarted mid-stop: every reading card under its row with no child of ours
// is handed back at its claim generation (not attempt), card@0 is guarded and never emitted,
// and the cards are not recovered when RUNNING again.
func TestAReaderMemberRestartedMidStopHandsBackItsReadingCards(t *testing.T) {
	t.Parallel()
	m, s, r, out := stopRig(Config{As: "reader.r1", Reader: true, Width: 4})
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
