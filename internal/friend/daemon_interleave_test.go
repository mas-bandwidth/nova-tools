package friend

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus2"
	"github.com/stretchr/testify/require"
)

type interleaveReadStore struct {
	*bus2.Fake
	beforeRead func()
}

func (s *interleaveReadStore) Read(ctx context.Context, stream, group, consumer string, block time.Duration, count int) ([]bus2.Entry, error) {
	if s.beforeRead != nil {
		s.beforeRead()
	}
	return s.Fake.Read(ctx, stream, group, consumer, block, count)
}

type interleaveDeliver func(context.Context, string) (int, error)

func (f interleaveDeliver) Deliver(ctx context.Context, text string) (int, error) {
	return f(ctx, text)
}

func interleaveRig(t *testing.T, asleep bool) *rig {
	t.Helper()
	r := newRig(t)
	r.d.StateDir = t.TempDir()
	_, err := UpdateSessionState(r.d.StateDir, func(s *SessionState) error {
		s.Coordinator, s.Asleep = "ada", asleep
		return nil
	})
	require.NoError(t, err)
	r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
	return r
}

func TestCoordinatorReadAfterConcurrentLocalSleepUsesCommittedState(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := interleaveRig(t, false)
		wake := r.send(t, "ada", "wake", "wake after the local sleep")
		store := &interleaveReadStore{Fake: r.store}
		reads := 0
		store.beforeRead = func() {
			reads++
			if reads == 1 {
				// The daemon already read awake state; Sleep commits while
				// its store read is in flight, before the message returns.
				_, err := UpdateSessionState(r.d.StateDir, func(s *SessionState) error { s.Asleep = true; return nil })
				require.NoError(t, err)
			}
		}
		r.d.Store = store
		calls := make(chan string, 4)
		r.d.Deliver = interleaveDeliver(func(_ context.Context, text string) (int, error) {
			calls <- text
			return 0, nil
		})
		r.run(t, 12)
		synctest.Wait()
		require.Len(t, calls, 1)
		require.Contains(t, <-calls, wake.ID)
		state, err := ReadSessionState(r.d.StateDir)
		require.NoError(t, err)
		require.False(t, state.Asleep)
		require.Empty(t, state.WakeBarrier)
	})
}

func TestDeferredResultAfterSleepAndCoordinatorWakeRestoresOldWorkAfterWake(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := interleaveRig(t, false)
		old := r.send(t, "bob", "old", "old deferred work")
		release := make(chan struct{})
		calls := make(chan string, 8)
		attempts := 0
		r.d.Deliver = interleaveDeliver(func(ctx context.Context, text string) (int, error) {
			calls <- text
			attempts++
			if attempts == 1 {
				select {
				case <-release:
					return 0, Deferred{Reason: "writer busy"}
				case <-ctx.Done():
					return 0, ctx.Err()
				}
			}
			return 0, nil
		})
		var wake bus2.Message
		r.at[1] = func() {
			synctest.Wait() // first delivery is blocked, not completed
			_, err := UpdateSessionState(r.d.StateDir, func(s *SessionState) error { s.Asleep = true; return nil })
			require.NoError(t, err)
			wake = r.send(t, "ada", "wake", "new coordinator work")
			close(release)
		}
		store := &interleaveReadStore{Fake: r.store}
		reads := 0
		store.beforeRead = func() {
			reads++
			if reads == 2 {
				// Make the old Deferred result available in the same loop
				// that reads the coordinator wake, before result handling.
				synctest.Wait()
			}
		}
		r.d.Store = store
		r.run(t, 20)
		synctest.Wait()
		require.Len(t, calls, 3)
		require.Contains(t, <-calls, old.ID)
		require.Contains(t, <-calls, wake.ID, "the coordinator takes the first attempt after waking")
		require.Contains(t, <-calls, old.ID, "the parked deferred delivery is restored afterward")
		require.NotContains(t, strings.Join(r.records, "\n"), "deliveries=1/3", "Deferred is not a failed attempt")
		pending, _, err := r.bus.PendingPage(context.Background(), "bob", DaemonConsumer, "", 100)
		require.NoError(t, err)
		require.Empty(t, pending)
	})
}

func TestFailedCoordinatorAttemptClearsBarrierThenHeldWorkRunsInNumericFIFO(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := interleaveRig(t, true)
		var held []bus2.Message
		for i := 0; i < 12; i++ {
			held = append(held, r.send(t, "bob", "held", fmt.Sprintf("held item %d", i)))
		}
		wake := r.send(t, "ada", "wake", "coordinator fails once")
		calls := make(chan string, 20)
		r.d.Deliver = interleaveDeliver(func(_ context.Context, text string) (int, error) {
			calls <- text
			if strings.Contains(text, "id="+wake.ID+" ") {
				return 1, nil // non-Deferred failure releases the priority barrier
			}
			return 0, nil
		})
		r.run(t, 50)
		synctest.Wait()
		require.Len(t, calls, 13)
		require.Contains(t, <-calls, wake.ID)
		for _, message := range held {
			require.Contains(t, <-calls, message.ID)
		}
		state, err := ReadSessionState(r.d.StateDir)
		require.NoError(t, err)
		require.Empty(t, state.WakeBarrier)
		pending, _, err := r.bus.PendingPage(context.Background(), "bob", DaemonConsumer, "", 100)
		require.NoError(t, err)
		require.Len(t, pending, 1, "held successes ACK; the failed coordinator remains pending")
		require.Equal(t, wake.ID, pending[0].Message().ID)
		require.Contains(t, strings.Join(r.records, "\n"), "deliveries=1/3")
	})
}
