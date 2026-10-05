package keepalive

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type loopStore struct {
	now       func() time.Time
	peer      *Machine
	pending   []Entry
	sent      []Frame
	headed    bool
	appendErr error
}

func (s *loopStore) Head(context.Context, string) (string, error) {
	s.headed = true
	return "initial", nil
}
func (s *loopStore) ReadBatch(_ context.Context, _ string, _ string, _ int) (Read, error) {
	out := s.pending
	s.pending = nil
	return Read{Entries: out, Next: "next"}, nil
}
func (s *loopStore) AppendBatch(_ context.Context, frames []Frame) ([]AppendResult, error) {
	if !s.headed {
		panic("append before initial head")
	}
	var out []AppendResult
	for _, f := range frames {
		s.sent = append(s.sent, f)
		if s.appendErr != nil {
			out = append(out, AppendResult{Unknown: true})
			continue
		}
		if s.peer != nil {
			if _, err := s.peer.Observe(s.now(), f); err != nil {
				return nil, err
			}
			reply, send, err := s.peer.Next(s.now(), false)
			if err != nil {
				return nil, err
			}
			if send {
				s.pending = append(s.pending, Entry{ID: "reply", Frame: reply})
			}
		}
		out = append(out, AppendResult{ID: "sent"})
	}
	return out, s.appendErr
}

func TestLoopExchangesDaemonFramesWithoutNativeDelivery(t *testing.T) {
	t.Parallel()
	for _, role := range []string{"coordinator", "friend"} {
		t.Run(role, func(t *testing.T) {
			t.Parallel()
			now := time.Unix(100, 0)
			seat := Seat{Holder: "seat", Epoch: 1, Generation: 1}
			local, peer, opposite := "seat", "worker", "friend"
			if role == "friend" {
				local, peer, opposite = "worker", "seat", "coordinator"
			}
			remote, err := New(peer, local, opposite, "remote", seat)
			require.NoError(t, err)
			store := &loopStore{now: func() time.Time { return now }, peer: remote}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var observed []Observation
			steps := 0
			l := Loop{Name: local, Role: role, Instance: "local", Store: store, Authority: func(context.Context) (Seat, error) { return seat, nil }, Peers: func(context.Context) ([]string, error) { return []string{peer}, nil }, Asleep: func(context.Context) (bool, error) { return true, nil }, HealthBatch: func(_ context.Context, o []Observation) error {
				observed = append(observed, o...)
				return errors.New("projection unavailable")
			}, Now: func() time.Time { return now }, Pause: func(context.Context, time.Duration) {
				steps++
				now = now.Add(time.Second)
				if steps == 4 {
					cancel()
				}
			}, Record: func(string) {}}
			require.NoError(t, l.Run(ctx))
			require.Len(t, store.sent, 4, "one new frame per second even while asleep")
			for _, f := range store.sent {
				assert.True(t, f.Asleep)
				assert.Equal(t, seat, f.Seat)
			}
			if role == "coordinator" {
				require.Len(t, observed, 4)
				assert.True(t, observed[3].Up)
				assert.NotZero(t, observed[3].Evidence)
			} else {
				assert.Empty(t, observed, "friends cannot project authoritative sprint health")
			}
		})
	}
}

func TestLoopStopsAuthoritativeWritesWhenAuthorityFails(t *testing.T) {
	t.Parallel()
	now := time.Unix(100, 0)
	store := &loopStore{now: func() time.Time { return now }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reads := 0
	steps := 0
	projections := 0
	var logs []string
	l := Loop{Name: "seat", Role: "coordinator", Instance: "local", Store: store, Authority: func(context.Context) (Seat, error) {
		reads++
		if reads > 2 {
			return Seat{}, errors.New("authority down")
		}
		return Seat{Holder: "seat", Generation: 1}, nil
	}, Peers: func(context.Context) ([]string, error) { return []string{"worker"}, nil }, HealthBatch: func(context.Context, []Observation) error { projections++; return nil }, Now: func() time.Time { return now }, Pause: func(context.Context, time.Duration) {
		steps++
		now = now.Add(time.Second)
		if steps == 3 {
			cancel()
		}
	}, Record: func(s string) { logs = append(logs, s) }}
	require.NoError(t, l.Run(ctx))
	assert.Len(t, store.sent, 1)
	assert.Equal(t, 1, projections)
	assert.NotEmpty(t, logs)
}

func TestLoopAdvancesMalformedEntryAndDoesNotBlindRetryUnknownAppend(t *testing.T) {
	t.Parallel()
	now := time.Unix(100, 0)
	store := &loopStore{now: func() time.Time { return now }, pending: []Entry{{ID: "bad", DecodeError: "invalid frame"}}, appendErr: errors.New("partial write")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	steps := 0
	var logs []string
	l := Loop{Name: "worker", Role: "friend", Instance: "local", Store: store, Authority: func(context.Context) (Seat, error) { return Seat{Holder: "seat", Generation: 1}, nil }, Now: func() time.Time { return now }, Pause: func(context.Context, time.Duration) {
		steps++
		now = now.Add(time.Second)
		if steps == 2 {
			cancel()
		}
	}, Record: func(s string) { logs = append(logs, s) }}
	require.NoError(t, l.Run(ctx))
	require.Len(t, store.sent, 2)
	assert.NotEqual(t, store.sent[0].Seq, store.sent[1].Seq, "unknown receipt leads to a new tick challenge, never blind replay")
	assert.Contains(t, logs, "keepalive decode: invalid frame")
}
