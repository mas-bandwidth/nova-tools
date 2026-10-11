package main

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
)

// dialRecord is one verb batch the fake dial was handed.
type dialRecord struct {
	client *http.Client
	verbs  [][]string
}

// TestSprintDialerBacksOffAndRebuildsTheClient pins the reader's re-dial
// (tla/LoopRedial.tla, ServerRestart, Redial, BackoffBounded): a refused or
// reset connection backs the next ask off one second doubling to the ceiling,
// every failed ask replaces the client so a dead connection is never reused,
// and an answer clears the backoff.
func TestSprintDialerBacksOffAndRebuildsTheClient(t *testing.T) {
	t.Parallel()
	var dials []dialRecord
	var waits []time.Duration
	failing := true
	d := newSprintDialer("server.example:6390")
	d.dial = func(_ context.Context, _ string, hc *http.Client, verbs ...[]string) ([]sprintwire.Result, error) {
		dials = append(dials, dialRecord{client: hc, verbs: verbs})
		if failing {
			return nil, errors.New("dial tcp: connect: connection refused")
		}
		return []sprintwire.Result{{Code: 0}}, nil
	}
	d.wait = func(_ context.Context, dur time.Duration) bool {
		waits = append(waits, dur)
		return true
	}
	ctx := context.Background()
	first := d.client

	// the first verb is asked at once, and each refused verb after it waits one
	// second doubling to the ceiling: 1, 2, 4, 8, 16, then 30
	for i := 0; i < 7; i++ {
		_, err := d.do(ctx, []string{"queue"})
		require.Error(t, err)
	}
	assert.Equal(t, []time.Duration{
		RedialFirst, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, RedialLast,
	}, waits, "the backoff doubles from %s to %s", RedialFirst, RedialLast)
	require.Len(t, dials, 7)
	assert.NotSame(t, first, dials[1].client, "the client that failed was not rebuilt")
	assert.NotSame(t, dials[1].client, dials[2].client, "every failed ask replaces the client")

	// the server answers after the ceiling wait: the backoff is cleared, so the
	// verb after it is asked at once
	failing = false
	waits = nil
	_, err := d.do(ctx, []string{"queue"})
	require.NoError(t, err)
	assert.Equal(t, []time.Duration{RedialLast}, waits, "the ask before a recovery waits the last backoff")

	failing = true
	waits = nil
	_, err = d.do(ctx, []string{"queue"})
	require.Error(t, err)
	assert.Empty(t, waits, "after an answer the first refusal is asked at once")

	// the refusal after the recovery starts the backoff over at one second
	waits = nil
	_, err = d.do(ctx, []string{"queue"})
	require.Error(t, err)
	assert.Equal(t, []time.Duration{RedialFirst}, waits, "the backoff starts over after an answer")
}

// TestSprintDialerWaitRespectsContext pins that a backoff never parks a loop
// past the caller's deadline: a context that ends during the wait ends the ask
// (tla/LoopRedial.tla, Backoff).
func TestSprintDialerWaitRespectsContext(t *testing.T) {
	t.Parallel()
	d := newSprintDialer("server.example:6390")
	calls := 0
	d.dial = func(context.Context, string, *http.Client, ...[]string) ([]sprintwire.Result, error) {
		calls++
		return nil, errors.New("connection reset by peer")
	}
	d.wait = func(ctx context.Context, _ time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
	_, _ = d.do(context.Background(), []string{"queue"}) // one failure leaves a backoff
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := d.do(ctx, []string{"queue"})
	require.ErrorIs(t, err, context.Canceled, "a wait cut by the context ends the ask")
	assert.Equal(t, 1, calls, "no verb was dialled once the context had ended")
}
