package friend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recoverHarness is a fake harness for the recovery tests: every delivery
// answers the outcome the test queued (the last repeats), and RecoverSession
// records the handoff and answers a fresh id. It is the seam a real adapter
// fills (OpenCode.RecoverSession). block makes a delivery print and then wait
// for its context to end, a turn that is output but no progress (stuck).
type recoverHarness struct {
	mu         sync.Mutex
	outcomes   []error
	n          int
	seeds      []string
	newIDs     []string
	recoverErr error
	block      bool
	advance    func() // called after each delivery: opens the claim for the next turn
}

func (h *recoverHarness) Deliver(ctx context.Context, _ string) (int, error) {
	h.mu.Lock()
	h.n++
	i := h.n - 1
	var err error
	if len(h.outcomes) > 0 {
		err = h.outcomes[min(i, len(h.outcomes)-1)]
	}
	block, advance := h.block, h.advance
	h.mu.Unlock()
	if block {
		Printed(ctx, []byte("working\n"))
		<-ctx.Done()
		return 0, ctx.Err()
	}
	if advance != nil {
		advance() // the message this turn failed on is claimable again
	}
	return 0, err
}

func (h *recoverHarness) RecoverSession(_ context.Context, seed string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seeds = append(h.seeds, seed)
	if h.recoverErr != nil {
		return "", h.recoverErr
	}
	id := fmt.Sprintf("new-%d", len(h.seeds))
	h.newIDs = append(h.newIDs, id)
	return id, nil
}

// handoffRig is a rig whose session can be replaced: the fake harness, the
// injected clock and the bus.Fake, no socket and no wall clock.
func handoffRig(t *testing.T, h *recoverHarness) *rig {
	t.Helper()
	r := newRig(t)
	r.d.Deliver, r.d.Recover = h, h
	r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
	r.d.Coordinator = "ada"
	return r
}

func (r *rig) recoveredStatus() (Status, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.status) - 1; i >= 0; i-- {
		if r.status[i].Session == SessionRecovered {
			return r.status[i], true
		}
	}
	return Status{}, false
}

func (r *rig) seedHandoff(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(QueueFile)), []byte(`{"tasks":[{"id":"card-1","state":"queued"}]}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "STATUS.md"), []byte("cairn: working card-1\n"), 0o644))
}

// TestBrokenSessionIsReplacedWithAHandoff pins session recovery
// (session-recovery-r-b.w2): a provider refusal streak, a context-limit
// error, a compaction loop and a stuck turn each open a fresh session whose
// first turn is the handoff (the queue, the newest cairn or status file, the
// pong line and every pending message); status says session=recovered
// from= to= reason= at=; the coordinator is told once; the fourth recovery in
// an hour stays broken and asks for a person; and no message is given up.
func TestBrokenSessionIsReplacedWithAHandoff(t *testing.T) {
	t.Parallel()

	t.Run("a provider refusal streak opens a fresh session with the handoff", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			h := &recoverHarness{outcomes: []error{
				ProviderRefused{Session: "old", Reason: "invalid_request_error: bad request"},
				ProviderRefused{Session: "old", Reason: "invalid_request_error: bad request"},
				nil,
			}}
			r := handoffRig(t, h)
			r.d.BrokenAfter = 2
			r.d.RecoverMax = 3
			h.advance = func() { r.store.Advance(bus.ClaimAfter) }
			r.seedHandoff(t, r.d.Dir)
			r.send(t, "ada", "hello", "are you there?")
			r.run(t, 20)

			require.Len(t, h.seeds, 1, "one fresh session")
			seed := h.seeds[0]
			assert.Contains(t, seed, "QUEUE.json")
			assert.Contains(t, seed, "card-1")
			assert.Contains(t, seed, "STATUS.md")
			assert.Contains(t, seed, "cairn: working card-1")
			assert.Contains(t, seed, "are you there?", "every pending message rides in the handoff")

			rec, ok := r.recoveredStatus()
			require.True(t, ok, "status says session=recovered: %+v", r.status)
			assert.Equal(t, "old", rec.SessionFrom)
			assert.Equal(t, "new-1", rec.SessionTo)
			assert.NotEmpty(t, rec.SessionReason)
			assert.False(t, rec.RecoveredAt.IsZero())

			got := r.adaGot(t)
			require.Len(t, got, 1, "the coordinator is told once: %v", got)
			assert.True(t, strings.HasPrefix(got[0], "friend bob: session old replaced by new-1:"), got[0])
			for _, line := range r.records {
				assert.NotContains(t, line, "given_up", "a recovery never gives up a message: %v", line)
			}
		})
	})

	t.Run("a context-limit error opens a fresh session", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			h := &recoverHarness{outcomes: []error{ContextLimit{Session: "old", Reason: "prompt is too long"}}}
			r := handoffRig(t, h)
			r.d.RecoverMax = 1
			r.seedHandoff(t, r.d.Dir)
			r.send(t, "ada", "hello", "x")
			r.run(t, 20)
			require.Len(t, h.seeds, 1)
			rec, ok := r.recoveredStatus()
			require.True(t, ok, "status says session=recovered")
			assert.Contains(t, rec.SessionReason, "context limit")
			assert.Contains(t, strings.Join(r.records, "\n"), "session=context_limit")
		})
	})

	t.Run("a compaction loop opens a fresh session", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			h := &recoverHarness{outcomes: []error{Compaction{Session: "old"}, Compaction{Session: "old"}, Compaction{Session: "old"}}}
			r := handoffRig(t, h)
			r.d.RecoverMax = 1
			h.advance = func() { r.store.Advance(bus.ClaimAfter) }
			r.seedHandoff(t, r.d.Dir)
			r.send(t, "ada", "hello", "x")
			r.run(t, 30)
			require.Len(t, h.seeds, 1)
			rec, ok := r.recoveredStatus()
			require.True(t, ok, "status says session=recovered")
			assert.Contains(t, rec.SessionReason, "compaction loop")
		})
	})

	t.Run("a turn stuck with output opens a fresh session", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			h := &recoverHarness{block: true}
			r := handoffRig(t, h)
			r.d.StuckAfter = 4 * BeatEvery
			r.d.RecoverMax = 1
			r.seedHandoff(t, r.d.Dir)
			r.send(t, "ada", "hello", "x")
			r.run(t, 20)
			require.Len(t, h.seeds, 1)
			rec, ok := r.recoveredStatus()
			require.True(t, ok, "status says session=recovered")
			assert.Contains(t, rec.SessionReason, "stuck")
			assert.Contains(t, strings.Join(r.records, "\n"), "stuck: output but no progress")
		})
	})

	t.Run("the fourth recovery in an hour stays broken and asks for a person", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			h := &recoverHarness{outcomes: []error{
				ProviderRefused{Session: "old", Reason: "invalid_request_error: bad"},
			}}
			r := handoffRig(t, h)
			r.d.BrokenAfter = 1
			r.d.RecoverMax = 3
			h.advance = func() { r.store.Advance(bus.ClaimAfter) }
			r.seedHandoff(t, r.d.Dir)
			r.send(t, "ada", "hello", "x")
			r.run(t, 120)

			assert.Len(t, h.seeds, 3, "three recoveries, the fourth is refused")
			got := r.adaGot(t)
			replaced, person := 0, 0
			for _, line := range got {
				if strings.Contains(line, "replaced by") {
					replaced++
				}
				if strings.Contains(line, "needs a person") {
					person++
				}
			}
			assert.Equal(t, 3, replaced, "one line per recovery: %v", got)
			assert.Equal(t, 1, person, "one line that a person is needed: %v", got)
			pending, _, err := r.bus.Peek(context.Background(), "bob")
			require.NoError(t, err)
			assert.Len(t, pending, 1, "the message is still pending, never given up")
			for _, line := range r.records {
				assert.NotContains(t, line, "given_up")
			}
		})
	})
}
