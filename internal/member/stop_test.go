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

// A stop-return the server refuses is said and tried again next pass; the lane stays held
// until the server takes it, and the card is never finished meanwhile.
func TestARefusedStopReturnIsTriedAgain(t *testing.T) {
	t.Parallel()
	m, s, r, out := stopRig(Config{As: "m", Width: 1})
	p := pk("c1")
	s.set("queue", 0, queueWith(t, "RUNNING", 7, ready("c1")))
	s.set("take", 0, takeJSON(t, p))
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	c := r.child("c1")
	s.set("queue", 0, queueWith(t, "STOPPED", 7, working("c1", 1, &p)))
	s.set("stop-return", 2, "nova-sprint stop-return: the machine is RUNNING")
	c.end(Result{OK: false})
	for range 2 {
		_, err = m.Tick(time.Unix(10, 0))
		require.NoError(t, err)
	}
	assert.Len(t, s.lines("stop-return"), 2, "one try per pass while refused")
	assert.Equal(t, 1, m.Running(), "the lane is held until the server takes the return")
	assert.Contains(t, out.String(), "STOP-RETURN refused card=c1 gen=1 pid=4000 exit=2: nova-sprint stop-return: the machine is RUNNING; tried again next pass")
	s.set("stop-return", 0, "STOP-RETURN OK c1@1 -> ready gen=2")
	_, err = m.Tick(time.Unix(20, 0))
	require.NoError(t, err)
	assert.Equal(t, 0, m.Running())
	assert.Empty(t, s.lines("finish"))
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
