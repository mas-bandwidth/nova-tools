package member

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type reviewBoundary struct {
	began, release chan struct{}
	once           sync.Once
}

func newReviewBoundary() *reviewBoundary {
	return &reviewBoundary{began: make(chan struct{}), release: make(chan struct{})}
}

func (b *reviewBoundary) hold() { close(b.began); <-b.release }
func (b *reviewBoundary) free() { b.once.Do(func() { close(b.release) }) }

func reviewBoundaryEvent(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	require.Eventually(t, func() bool {
		select {
		case <-ch:
			return true
		default:
			return false
		}
	}, 10*time.Second, time.Millisecond, "background boundary event did not arrive")
}

type reviewHeldRunner struct {
	*fakeRunner
	*reviewBoundary
}

func (r *reviewHeldRunner) Start(p Packet) (Child, error) {
	r.hold()
	ch, err := r.fakeRunner.Start(p)
	if err == nil {
		r.child(p.Card).end(Result{})
	}
	return ch, err
}

type reviewHeldResult struct{ *reviewBoundary }

func (c *reviewHeldResult) Done() bool { return true }
func (c *reviewHeldResult) Result() Result {
	c.hold()
	return Result{}
}

// reviewBoundaryTick joins the pass even when an assertion fails. Releasing the
// held work also lets an accidentally inline implementation finish its pass.
func reviewBoundaryTick(t *testing.T, g *rig, b *reviewBoundary) {
	t.Helper()
	done := make(chan struct{})
	var err error
	posted := false
	t.Cleanup(func() {
		b.free()
		reviewBoundaryEvent(t, done)
		if !posted {
			reviewBoundaryEvent(t, g.m.Wake())
		}
	})
	go func() {
		_, err = g.m.Tick(time.Unix(0, 0))
		close(done)
	}()
	reviewBoundaryEvent(t, b.began)
	reviewBoundaryEvent(t, done)
	require.NoError(t, err)
	assert.Equal(t, 1, g.m.Running())
	assert.True(t, g.m.running["c1"].busy)
	g.m.Drain()
	p := pk("c1")
	if _, starting := g.m.runner.(*reviewHeldRunner); starting {
		p.Gen++
		g.s.set("queue", 0, queueJSON(t, 7, working("c1", p.Gen, &p)))
	} else {
		g.s.set("queue", 0, queueJSON(t, 7))
	}
	_, err = g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, 1, g.m.Running(), "busy work keeps its claim through drain and a queue change")
	assert.Empty(t, g.r.endedLaunches())
	assert.Empty(t, g.s.lines("finish"))
	b.free()
	reviewBoundaryEvent(t, g.m.Wake())
	posted = true
	_, err = g.tick(t)
	require.NoError(t, err)
	assert.Zero(t, g.m.Running())
	assert.Len(t, g.r.endedLaunches(), 1)
	assert.Empty(t, g.s.lines("finish"), "obsolete work is reaped, never reported")
}

func TestBackgroundReviewTickDoesNotWaitForStart(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 1, Background: true})
	b := newReviewBoundary()
	g.m.runner = &reviewHeldRunner{g.r, b}
	p := pk("c1")
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", p.Gen, &p)))
	reviewBoundaryTick(t, g, b)
}

func TestBackgroundReviewTickDoesNotWaitForResult(t *testing.T) {
	t.Parallel()
	g, _ := pushRig(t, &fakePusher{}, Result{})
	b := newReviewBoundary()
	l := g.m.running["c1"]
	l.child = &reviewHeldResult{b}
	g.m.running["c1"] = l
	g.m.cfg.Background = true
	select {
	case <-g.m.Wake(): // discard the synchronous setup start
	default:
	}
	reviewBoundaryTick(t, g, b)
}
