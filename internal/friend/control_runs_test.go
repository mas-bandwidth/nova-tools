package friend

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type controlContextHarness struct {
	*lanesHarness
	contextsMu sync.Mutex
	anchors    []string
	gated      []bool
}

func (h *controlContextHarness) observe(ctx context.Context) {
	h.contextsMu.Lock()
	defer h.contextsMu.Unlock()
	h.anchors = append(h.anchors, processGroupAnchor(ctx))
	h.gated = append(h.gated, processStarted(ctx) != nil)
}

func (h *controlContextHarness) OpenSession(ctx context.Context, seed string) (string, error) {
	h.observe(ctx)
	return h.lanesHarness.OpenSession(ctx, seed)
}

func (h *controlContextHarness) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
	h.observe(ctx)
	return h.lanesHarness.DeliverTo(ctx, session, text)
}

// Removing either production control-context binding makes a real adapter's
// cancellation unable to prove descendant exit even while card work is held.
func TestStoppedOneShotControlRunsHaveIndependentNativeLaunchReceipts(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := &lanesHarness{dir: t.TempDir(), active: map[string]int{}}
		r, _ := laneRig(t, h, 2)
		observed := &controlContextHarness{lanesHarness: h}
		r.d.Deliver = observed
		r.d.MachineStopped = func() bool { return true }
		r.send(t, "ada", "control", "read this without taking a card")
		r.run(t, 8)
		assert.GreaterOrEqual(t, len(observed.anchors), 3, "two session opens and a cardless message must be receipted")
		seen := map[string]bool{}
		for n, path := range observed.anchors {
			assert.NotEmpty(t, path)
			assert.False(t, seen[path], "another run must not overwrite this group's receipt")
			seen[path] = true
			assert.True(t, observed.gated[n], "the harness cannot run before the native receipt is durable")
		}
	})
}

// delayedControlCleanup distinguishes observing cancellation from actually
// reaping the native group, so a daemon return cannot be mistaken for cleanup.
type delayedControlCleanup struct {
	*lanesHarness
	open                       bool
	started, canceled, cleanup chan struct{}
}

func (h *delayedControlCleanup) wait(ctx context.Context) error {
	close(h.started)
	<-ctx.Done()
	close(h.canceled)
	<-h.cleanup
	return ctx.Err()
}

func (h *delayedControlCleanup) OpenSession(ctx context.Context, seed string) (string, error) {
	if h.open {
		return "", h.wait(ctx)
	}
	return h.lanesHarness.OpenSession(ctx, seed)
}

func (h *delayedControlCleanup) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
	return LaneTurn{Exit: -1}, h.wait(ctx)
}

func TestDaemonReturnWaitsForCardlessAdapterCleanup(t *testing.T) {
	t.Parallel()
	for _, open := range []bool{true, false} {
		name := "message"
		if open {
			name = "session-open"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				base := &lanesHarness{dir: t.TempDir(), active: map[string]int{}}
				h := &delayedControlCleanup{lanesHarness: base, open: open, started: make(chan struct{}), canceled: make(chan struct{}), cleanup: make(chan struct{})}
				r, _ := laneRig(t, base, 1)
				r.d.Deliver = h
				r.d.MachineStopped = func() bool { return true }
				advance := make(chan struct{})
				r.d.Pause = func(ctx context.Context, _ time.Duration) {
					select {
					case <-ctx.Done():
					case <-advance:
					}
				}
				r.send(t, "ada", "control", "remain stopped")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				r.cancel = cancel
				done := make(chan error, 1)
				go func() { done <- r.d.Run(ctx) }()
			started:
				for {
					select {
					case <-h.started:
						break started
					case advance <- struct{}{}:
					}
				}
				cancel()
				<-h.canceled
				synctest.Wait()
				assert.Empty(t, done, "Run must wait for native cleanup, even after the adapter observes cancellation")
				close(h.cleanup)
				require.NoError(t, <-done)
			})
		})
	}
}
