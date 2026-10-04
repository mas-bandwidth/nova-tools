package friend

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// edgeAckStore fails one acknowledgement without changing the pending entry.
type edgeAckStore struct {
	*bus.Fake
	attempts int
}

func (s *edgeAckStore) Ack(ctx context.Context, stream, group string, entries ...string) (int64, error) {
	s.attempts++
	if s.attempts == 1 {
		return 0, errors.New("ack store unavailable")
	}
	return s.Fake.Ack(ctx, stream, group, entries...)
}

func TestSuccessfulTurnRemainsRetryableAfterAcknowledgementFailure(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		store := &edgeAckStore{Fake: r.store}
		r.d.Store = store
		r.passive = true // the injected pause synchronizes the active worker below
		r.d.Pause = func(context.Context, time.Duration) {
			synctest.Wait()
			select {
			case <-r.gate: // consume the completed turn before another delivery uses the rig
			default:
			}
		}
		r.send(t, "ada", "hello", "retry the acknowledgement")
		r.at[10] = func() { r.store.Advance(bus.ClaimAfter) }
		r.run(t, 40)
		assert.GreaterOrEqual(t, store.attempts, 2, "a failed ACK must not leave the known-entry map suppressing retry forever")
		pending, fresh, err := r.bus.Peek(context.Background(), "bob")
		require.NoError(t, err)
		assert.Empty(t, pending)
		assert.Empty(t, fresh)
		assert.Equal(t, 1, r.last().Delivered, "only the acknowledged success counts")
		joined := strings.Join(r.records, "\n")
		assert.Contains(t, joined, "ack=failed")
		assert.Contains(t, joined, "acked=true")
	})
}

func TestPassiveAsleepMonitorLeavesMessagesForInteractiveReceive(t *testing.T) {
	t.Parallel()
	for _, from := range []string{"bob", "ada"} {
		t.Run(from, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			r.d.Deliver, r.d.Harness, r.passive = Stub{Harness: "fake"}, "fake", true
			r.d.StateDir = t.TempDir()
			_, err := UpdateSessionState(r.d.StateDir, func(s *SessionState) error {
				s.Asleep, s.Coordinator = true, "ada"
				return nil
			})
			require.NoError(t, err)
			r.send(t, "bob", "interactive", "already held by the interactive reader")
			held, ok, err := r.bus.Recv(context.Background(), "bob", 0)
			require.NoError(t, err)
			require.True(t, ok)
			fresh := r.send(t, from, "hello", "left for the session's own receive")
			r.run(t, 4)
			assert.Empty(t, r.delivered)
			own, cursor, err := r.bus.PendingPage(context.Background(), "bob", DaemonConsumer, "", 100)
			require.NoError(t, err)
			assert.Empty(t, own, "a passive monitor never claims a stream entry")
			assert.Empty(t, cursor)
			interactive, _, err := r.bus.PendingPage(context.Background(), "bob", bus.Consumer, "", 100)
			require.NoError(t, err)
			require.Len(t, interactive, 1)
			assert.Equal(t, held.Entry, interactive[0].Entry, "the existing interactive delivery stays pending and owned")
			next, ok, err := r.bus.Recv(context.Background(), "bob", 0)
			require.NoError(t, err)
			require.True(t, ok, "the passive monitor leaves the fresh message unclaimed and unacknowledged")
			assert.Equal(t, fresh.ID, next.Message().ID)
			state, err := ReadSessionState(r.d.StateDir)
			require.NoError(t, err)
			assert.Empty(t, state.WakeBarrier, "a passive observation cannot create a native-delivery barrier")
			assert.Equal(t, from != "ada", state.Asleep, "only the configured coordinator's message wakes the saved bit")
		})
	}
}
