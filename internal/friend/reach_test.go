package friend

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReachClimbsTheLadderOnlyUntilProof(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	var did []ReachStep
	r := Reach{
		Now:  func() time.Time { return now },
		Wait: func(context.Context, time.Duration) { now = now.Add(time.Second) },
		Do: func(_ context.Context, step ReachStep, _ string) (string, string, error) {
			did = append(did, step)
			return string(step), "", nil
		},
		Proof: func(_ context.Context, _ string) (string, bool, error) { return "message", len(did) == 2, nil },
	}
	step, ok, err := r.Run(context.Background(), ReachBus, "nonce", time.Minute)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, ReachPush, step)
	assert.Equal(t, []ReachStep{ReachBus, ReachPush}, did)
}

func TestReachSkipsAnUnavailableStep(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	var did []ReachStep
	r := Reach{
		Now:  func() time.Time { return now },
		Wait: func(context.Context, time.Duration) { now = now.Add(time.Second) },
		Do: func(_ context.Context, step ReachStep, _ string) (string, string, error) {
			did = append(did, step)
			if step == ReachPush {
				return "", "daemon is not up", nil
			}
			return string(step), "", nil
		},
		Proof: func(_ context.Context, _ string) (string, bool, error) {
			return "pong", did[len(did)-1] == ReachWindow, nil
		},
	}
	step, ok, err := r.Run(context.Background(), ReachBus, "nonce", time.Minute)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, ReachWindow, step)
	assert.Equal(t, []ReachStep{ReachBus, ReachPush, ReachWindow}, did)
}

func TestReachCapsTheLastWaitToTheRungsRemainingTime(t *testing.T) {
	t.Parallel()
	now := t0
	var waits []time.Duration
	r := Reach{Now: func() time.Time { return now }, Wait: func(_ context.Context, d time.Duration) { waits = append(waits, d); now = now.Add(d) }, Do: func(context.Context, ReachStep, string) (string, string, error) { return "x", "", nil }, Proof: func(context.Context, string) (string, bool, error) { return "", false, nil }}
	_, ok, err := r.Run(context.Background(), ReachWindow, "n", 1500*time.Millisecond)
	require.NoError(t, err); assert.False(t, ok); assert.Equal(t, []time.Duration{time.Second, 500 * time.Millisecond}, waits)
}

func TestReachCancellationStopsBeforeANewEffect(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background()); now, calls := t0, 0
	r := Reach{Now: func() time.Time { return now }, Wait: func(context.Context, time.Duration) { cancel() }, Do: func(context.Context, ReachStep, string) (string, string, error) { calls++; return "x", "", nil }, Proof: func(context.Context, string) (string, bool, error) { return "", false, nil }}
	_, _, err := r.Run(ctx, ReachBus, "n", time.Second)
	require.ErrorIs(t, err, context.Canceled); assert.Equal(t, 1, calls)
}

func TestReachProofBetweenRungsPreventsTheNextEffect(t *testing.T) {
	t.Parallel()
	now, calls, proofs := t0, 0, 0
	r := Reach{Now: func() time.Time { return now }, Wait: func(context.Context, time.Duration) { now = now.Add(time.Second) }, Do: func(context.Context, ReachStep, string) (string, string, error) { calls++; return "x", "", nil }, Proof: func(context.Context, string) (string, bool, error) { proofs++; return "pong", proofs > 2, nil }}
	step, ok, err := r.Run(context.Background(), ReachBus, "n", time.Second)
	require.NoError(t, err); assert.True(t, ok); assert.Equal(t, ReachPush, step); assert.Equal(t, 1, calls)
}
