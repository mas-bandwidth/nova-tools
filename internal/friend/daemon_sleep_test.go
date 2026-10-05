package friend

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/require"
)

func asleepRig(t *testing.T, barrier string) *rig {
	t.Helper()
	r := newRig(t)
	r.gate = make(chan struct{}, 2048)
	r.d.StateDir = t.TempDir()
	_, err := UpdateSessionState(r.d.StateDir, func(s *SessionState) error {
		s.Coordinator = "ada"
		s.Asleep = true
		s.WakeBarrier = barrier
		return nil
	})
	require.NoError(t, err)
	r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
	return r
}

func TestDaemonSleepHoldsWithoutAttempt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := asleepRig(t, "")
		r.send(t, "bob", "held", "ordinary")
		r.run(t, 8)
		require.Empty(t, r.delivered)
		entries, _, err := r.bus.PendingPage(context.Background(), "bob", DaemonConsumer, "", 100)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.True(t, r.last().Asleep)
	})
}

func TestAsleepPingReplyReflectsCoordinatorAuthority(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		from       string
		wantAsleep bool
	}{
		{name: "noncoordinator", from: "bob", wantAsleep: true},
		{name: "coordinator", from: "ada", wantAsleep: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				r := asleepRig(t, "")
				ping := r.send(t, tc.from, "ping", "PING n1\nseat=ada since=2026-10-04T03:00:00Z\n")
				r.run(t, 8)
				state, err := ReadSessionState(r.d.StateDir)
				require.NoError(t, err)
				require.Equal(t, tc.wantAsleep, state.Asleep)
				if tc.wantAsleep {
					require.Empty(t, r.delivered, "noncoordinator PING cannot start a session turn")
					require.Empty(t, r.d.m.Nonce, "noncoordinator PING does not change the challenge")
					require.Empty(t, state.WakeBarrier)
				} else {
					require.Empty(t, r.delivered, "a ping is answered and acked by the daemon, never a turn")
					require.Empty(t, state.WakeBarrier, "a ping that wakes the session is no barrier: it is acked at once")
					require.NotEqual(t, Quiet, r.d.m.Challenge, "the authorized PING reaches the machine")
				}
				stream, err := r.store.Range(context.Background(), bus.StreamOf(tc.from), "-", "+", 100)
				require.NoError(t, err)
				var daemonPongs []string
				for _, e := range stream {
					msg := e.Message()
					if msg.Subject == DaemonPongSubject && msg.Re == ping.ID {
						daemonPongs = append(daemonPongs, msg.Body)
					}
				}
				require.Len(t, daemonPongs, 1)
				if tc.wantAsleep {
					require.Contains(t, daemonPongs[0], "asleep=true")
				} else {
					require.NotContains(t, daemonPongs[0], "asleep=true", "coordinator wake is durable before its daemon pong")
				}
			})
		})
	}
}

func TestDaemonSleepLargeBacklogCoordinatorFirst(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := asleepRig(t, "")
		for i := 0; i < 1001; i++ {
			r.send(t, "bob", "held", "ordinary")
		}
		r.send(t, "ada", "wake", "coordinator wake")
		r.run(t, 14)
		synctest.Wait()
		r.mu.Lock()
		delivered := append([]string(nil), r.delivered...)
		r.mu.Unlock()
		require.NotEmpty(t, delivered)
		require.Contains(t, delivered[0], "coordinator wake")
		state, err := ReadSessionState(r.d.StateDir)
		require.NoError(t, err)
		require.False(t, state.Asleep)
	})
}

func TestDaemonRestartRememberedBarrierDoesNotWake(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := asleepRig(t, "")
		r.send(t, "bob", "held", "ordinary")
		r.send(t, "ada", "wake", "old coordinator")
		entries, err := r.bus.RecvBatch(context.Background(), "bob", DaemonConsumer, 0, 2)
		require.NoError(t, err)
		require.Len(t, entries, 2)
		_, err = UpdateSessionState(r.d.StateDir, func(s *SessionState) error { s.WakeBarrier = entries[1].Entry; return nil })
		require.NoError(t, err)
		r.run(t, 4)
		require.Empty(t, r.delivered)
		state, err := ReadSessionState(r.d.StateDir)
		require.NoError(t, err)
		require.True(t, state.Asleep)
	})
}

func TestDaemonRestartAwakeBarrierBeforeHeld(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := asleepRig(t, "")
		r.send(t, "bob", "held", "ordinary")
		r.send(t, "ada", "wake", "owed coordinator")
		entries, err := r.bus.RecvBatch(context.Background(), "bob", DaemonConsumer, 0, 2)
		require.NoError(t, err)
		require.Len(t, entries, 2)
		_, err = UpdateSessionState(r.d.StateDir, func(s *SessionState) error { s.Asleep = false; s.WakeBarrier = entries[1].Entry; return nil })
		require.NoError(t, err)
		r.run(t, 5)
		require.NotEmpty(t, r.delivered)
		require.Contains(t, r.delivered[0], "owed coordinator")
	})
}

func TestDaemonMissingBarrierRefuses(t *testing.T) {
	t.Parallel()
	r := asleepRig(t, "123-0")
	err := r.d.Run(context.Background())
	require.ErrorContains(t, err, "missing from daemon-owned pending")
	require.Empty(t, r.delivered)
}

func TestDaemonSingletonCannotChangeCoordinator(t *testing.T) {
	t.Parallel()
	r := asleepRig(t, "")
	lock, err := TakeDaemonLock(r.d.StateDir, "bob")
	require.NoError(t, err)
	defer lock.Unlock()
	r.d.Coordinator = "bob"
	err = r.d.Run(context.Background())
	require.Error(t, err)
	state, err := ReadSessionState(r.d.StateDir)
	require.NoError(t, err)
	require.Equal(t, "ada", state.Coordinator)
}

func TestDaemonEntryOrderNumeric(t *testing.T) {
	t.Parallel()
	require.True(t, entryBefore("9-100", "10-0"))
	require.True(t, entryBefore("10-2", "10-11"))
	require.False(t, entryBefore("10-11", "10-2"))
}

func TestPassiveObservedCoordinatorDoesNotUndoLaterLocalSleep(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := asleepRig(t, "")
		r.d.Deliver = Stub{Harness: "fake"}
		r.passive = true
		r.send(t, "ada", "wake", "one observed coordinator message")
		r.at[1] = func() {
			state, err := ReadSessionState(r.d.StateDir)
			require.NoError(t, err)
			require.False(t, state.Asleep)
			_, err = UpdateSessionState(r.d.StateDir, func(s *SessionState) error { s.Asleep = true; return nil })
			require.NoError(t, err)
		}
		r.run(t, 5)
		state, err := ReadSessionState(r.d.StateDir)
		require.NoError(t, err)
		require.True(t, state.Asleep)
		require.Empty(t, state.WakeBarrier)
	})
}
