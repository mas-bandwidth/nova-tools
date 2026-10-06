package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// movingFence is the in-memory store with another writer at work beside the
// tick: each read of the fence, while moves are left, is followed by that
// writer's step, so the read after it finds the fence moved.
type movingFence struct {
	*Mem
	moves int
	other func()
}

func (m *movingFence) ReadFence(ctx context.Context) (Fence, error) {
	f, err := m.Mem.ReadFence(ctx)
	if m.moves > 0 {
		m.moves--
		m.other()
	}
	return f, err
}

// A tick whose read lost the fence to other operations ("the sprint is busy:
// other operations kept the fence moving", the live server on 2026-10-06 under
// load) runs its parts again within the same tick, up to TickBusyRetries times,
// before it counts as failed: one busy read is a retry, never a failed tick on
// the heartbeat; a fence that keeps moving through every retry fails the tick
// once. One read of the fence a try (Attempts 1) makes each lost read a busy
// refusal, so the counts are exact; no clock, no socket.
func TestAFenceBusyTickRetriesBeforeFailing(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		moves   int
		retries int
		failed  bool
	}{
		// the tick's first read of the fence moves it harmlessly (it reads only
		// what is pending); the second is a fenced read's first, and the read
		// after it finds the fence moved: one busy try, then the parts pass
		{"one busy try", 2, 1, false},
		{"busy through every try", 1 << 30, TickBusyRetries, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(2)
			h.startMachine()
			other := &Store{B: h.m, Names: h.st.Names, Actor: "other", Now: h.st.Now, NewID: h.st.NewID, Sleep: h.st.Sleep}
			mf := &movingFence{Mem: h.m, moves: c.moves, other: func() {
				_, err := other.Run(h.ctx, Step{Verb: "world", Actor: "other", Plan: func(s *sprint.Snapshot) sprint.Plan {
					return sprint.Plan{Notes: []sprint.Note{{Kind: sprint.Happened, Type: "world", At: s.Now}}}
				}})
				require.NoError(t, err, "the other writer's step")
			}}
			loop := *h.st
			loop.tw = nil
			loop.B = mf
			loop.Attempts = 1
			loop.CheckTwin = nil
			res, err := loop.Tick(h.ctx)
			require.Equal(t, c.retries, res.BusyRetries, "the tick's tries again: %v", err)
			_, hb, merr := h.st.Machine(h.ctx)
			require.NoError(t, merr)
			if c.failed {
				require.True(t, IsFenceBusy(err), "a fence that never stops moving fails the tick busy: %v", err)
				require.Equal(t, 1, hb.Failures, "the tick failed once, its tries again within it: %+v", hb)
				return
			}
			require.NoError(t, err, "the tick's try after the busy read passes")
			require.Zero(t, hb.Failures, "a busy read retried within the tick is no failed tick: %+v", hb)
			require.Empty(t, hb.Error)
		})
	}
}

// The heartbeat keeps the count of ticks given up past their deadline, and a
// tick after keeps it.
func TestTheHeartbeatCountsTickOverruns(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	for want := int64(1); want <= 2; want++ {
		n, err := h.st.CountTickOverrun(h.ctx)
		require.NoError(t, err)
		require.Equal(t, want, n)
	}
	h.machine()
	_, hb, err := h.st.Machine(h.ctx)
	require.NoError(t, err)
	require.Equal(t, int64(2), hb.TickOverrun, "a tick keeps the count: %+v", hb)
}
