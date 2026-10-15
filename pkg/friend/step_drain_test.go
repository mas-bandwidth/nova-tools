package friend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A step applies every ended result already waiting: one select took at most one, so
// k lanes that ended together were seen over k beats (and their lanes held that long).
// The step now drains the lane and read channels, and the outbox finishes, until
// none has a value, capped by maxStepResults, so the daemon's state stays single-writer.
func TestAStepAppliesEveryEndedLaneResultWaitingOnTheChannels(t *testing.T) {
	t.Parallel()
	d := &Daemon{Friend: "bob", SaveLanes: func(LaneState) error { return nil }, Record: func(string) {}}
	l := &loop{d: d, ctx: context.Background(),
		outboxRes: make(chan outboxResult, 8),
		lanes:     &laneSet{results: make(chan laneResult, 8), state: LaneState{Sessions: map[int]string{}}},
		reads:     newReadSet()}
	for n := 1; n <= 4; n++ {
		l.lanes.results <- laneResult{ln: &lane{n: n}, open: true, session: fmt.Sprintf("ses_%d", n)}
	}
	l.drainResults(t0)
	require.Len(t, l.lanes.state.Sessions, 4, "every lane result waiting on the channel is applied in the one step")
	for n := 1; n <= 4; n++ {
		assert.Equal(t, fmt.Sprintf("ses_%d", n), l.lanes.state.Sessions[n])
	}
}

// Two outbox finishes are independent network work, one per card: each blocks until the
// other has started, and both finish, so the step never waits behind a finish. A serial
// finish deadlocks on the barrier and neither ends.
func TestTwoOutboxFinishesRunSideBySide(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		dir := r.d.Dir
		row := &twinRow{}
		r.d.Held = row.held
		const head = "0123456789abcdef0123456789abcdef01234567"
		a, b := workCard("a.w1", "working"), workCard("b.w1", "working")
		for _, c := range []HeldCard{a, b} {
			inboxJob(t, dir, c.Job, c.Brief)
			outboxReport(t, dir, c.Job, "Verdict: LAND\nHead: "+head+"\n\n"+c.Card+".\n")
		}
		row.set(a, b)

		var mu sync.Mutex
		started, done := 0, 0
		both := make(chan struct{})
		r.d.Finish = func(_ context.Context, _ []string) error {
			mu.Lock()
			started++
			second := started == 2
			mu.Unlock()
			if second {
				close(both)
			}
			// a serial finish blocks here with the other never started, the step
			// waits behind it, and synctest's virtual clock reports the deadlock
			// without a wall-clock bound
			<-both
			mu.Lock()
			done++
			mu.Unlock()
			return nil
		}
		r.run(t, 6)

		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, 2, started, "both finishes started")
		assert.Equal(t, 2, done, "both finishes saw the other start and ended: neither waited for the other to end")
		assert.True(t, r.d.outbox.finished[a.Job] && r.d.outbox.finished[b.Job], "both finishes ended and were applied")
	})
}

// A lane freed in a step is taken in the same step, while a finish is still in flight:
// the finish blocks until the lane has delivered, so a step that waited behind the finish
// could never deliver and the finish would never return (the package timeout is red).
func TestALaneIsTakenInTheStepWhileAFinishIsInFlight(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		dir := r.d.Dir
		row := &twinRow{}
		r.d.Held = row.held
		fin, work := workCard("fin.w1", "working"), workCard("work.w1", "working")
		inboxJob(t, dir, work.Job, work.Brief)
		outboxReport(t, dir, fin.Job, "Verdict: FAIL\n\nstuck.\n")
		row.set(fin, work)

		delivered := make(chan struct{})
		r.d.Deliver = &barrierLanes{delivered: delivered}
		r.passive = true
		r.d.Row = func() (string, int) { return ModeOneShot, 1 }
		r.d.CardDone = func(card, to string) string { return "nova-bus send --as bob --to " + to }
		// the lane already holds its session, so the freed lane takes the card on the
		// first step without waiting for an open's result: the finish is the only
		// thing off the loop the take could wait behind.
		laneState := LaneState{Sessions: map[int]string{1: "ses_1"}}
		r.d.LoadLanes = func() (LaneState, error) { return laneState, nil }
		r.d.SaveLanes = func(s LaneState) error { laneState = s; return nil }

		var mu sync.Mutex
		finished := false
		r.d.Finish = func(_ context.Context, _ []string) error {
			// the lane must deliver while this finish is in flight: a step that
			// waited behind the finish would deadlock and synctest would report it
			<-delivered
			mu.Lock()
			finished = true
			mu.Unlock()
			return nil
		}
		r.run(t, 6)

		mu.Lock()
		defer mu.Unlock()
		assert.True(t, finished, "the lane was taken while the finish was still in flight")
	})
}

// barrierLanes is a lane harness that opens one session and closes delivered when it is
// handed the card, so a finish can wait for the lane to run.
type barrierLanes struct{ delivered chan struct{} }

func (h *barrierLanes) Deliver(context.Context, string) (int, error) { return 0, nil }

func (h *barrierLanes) OpenSession(context.Context, string) (string, error) { return "ses_1", nil }

func (h *barrierLanes) DeliverTo(_ context.Context, _, text string) (LaneTurn, error) {
	if strings.Contains(text, "work.w1") {
		select {
		case <-h.delivered:
		default:
			close(h.delivered)
		}
	}
	return LaneTurn{Exit: 0}, nil
}

// A finish the server refused is not marked finished, and the next step sends it again:
// the answer comes back on the finishes' channel and the loop applies it, never the goroutine.
func TestARefusedOutboxFinishIsSentAgainOnALaterStep(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	dir := r.d.Dir
	row := &twinRow{}
	r.d.Held = row.held
	const head = "0123456789abcdef0123456789abcdef01234567"
	a := workCard("a.w1", "working")
	inboxJob(t, dir, a.Job, a.Brief)
	outboxReport(t, dir, a.Job, "Verdict: LAND\nHead: "+head+"\n\na.\n")
	row.set(a)

	var mu sync.Mutex
	sent := 0
	r.d.Finish = func(_ context.Context, _ []string) error {
		mu.Lock()
		sent++
		n := sent
		mu.Unlock()
		if n == 1 {
			return errors.New("the server refused the finish")
		}
		return nil
	}
	r.run(t, 3)
	mu.Lock()
	require.Equal(t, 1, sent, "the finish is sent once")
	mu.Unlock()
	assert.False(t, r.d.outbox.finished[a.Job], "a refused finish is not marked finished")

	r.run(t, 65) // past OutboxRetry
	mu.Lock()
	assert.Equal(t, 2, sent, "a refused finish is sent again on a later step")
	mu.Unlock()
	assert.True(t, r.d.outbox.finished[a.Job], "the finish the server took is then marked finished")
}
