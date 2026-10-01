//go:build functional

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type lockReplyClient struct {
	redis.UniversalClient
	cut string
	err error
}

func (c lockReplyClient) SetNX(ctx context.Context, key string, value any, expiration time.Duration) *redis.BoolCmd {
	r := c.UniversalClient.SetNX(ctx, key, value, expiration)
	if c.cut == "claim reply" && r.Err() == nil {
		return redis.NewBoolResult(r.Val(), c.err)
	}
	return r
}

func (c lockReplyClient) Incr(ctx context.Context, key string) *redis.IntCmd {
	if c.cut == "before advance" {
		return redis.NewIntResult(0, c.err)
	}
	r := c.UniversalClient.Incr(ctx, key)
	if c.cut == "advance reply" && r.Err() == nil {
		return redis.NewIntResult(r.Val(), c.err)
	}
	return r
}

// An interrupted reservation has written no table data. It still excludes
// ordinary writers until exact-owner cleanup or the existing grace repair.
func TestRedisWorkerReservationSurvivesInterruptedReplies(t *testing.T) {
	t.Parallel()
	for _, cut := range []string{"claim reply", "before advance", "advance reply"} {
		t.Run(cut, func(t *testing.T) {
			t.Parallel()
			st, client := liveStore(t)
			ctx := context.Background()
			r := st.B.(*Redis)
			before, err := r.ReadFence(ctx)
			require.NoError(t, err)
			lost := errors.New("reservation reply lost")
			broken := *r
			broken.C = lockReplyClient{UniversalClient: client, cut: cut, err: lost}
			op := OpRecord{ID: "worker-lock", Verb: "finish lock", Lock: true, At: time.Now()}
			_, err = broken.Lock(ctx, op)
			require.ErrorIs(t, err, lost)
			f, err := r.ReadFence(ctx)
			require.NoError(t, err)
			require.NotNil(t, f.Pending)
			require.Equal(t, op.ID, f.Pending.ID)
			wantGen := before.Gen
			if cut == "advance reply" {
				wantGen++
			}
			require.Equal(t, wantGen, f.Gen)
			acquired, err := r.Acquire(ctx, f.Gen, OpRecord{ID: "competing-writer"})
			require.NoError(t, err)
			require.False(t, acquired)
			require.NoError(t, r.Release(ctx, op, false))

			successor := OpRecord{ID: "successor-lock", Verb: "take lock", Lock: true, At: time.Now()}
			held, err := r.Lock(ctx, successor)
			require.NoError(t, err)
			require.True(t, held)
			acquired, err = r.Relock(ctx, op.ID, OpRecord{ID: "late-old-operation"})
			require.NoError(t, err)
			require.False(t, acquired)
			require.NoError(t, r.Release(ctx, op, false))
			f, err = r.ReadFence(ctx)
			require.NoError(t, err)
			require.NotNil(t, f.Pending)
			require.Equal(t, successor.ID, f.Pending.ID, "old cleanup cannot release its successor")
			require.Equal(t, wantGen+1, f.Gen)
			require.NoError(t, r.Release(ctx, successor, false))
		})
	}
}

func TestRedisWorkerReservationsStayInTheirEpoch(t *testing.T) {
	t.Parallel()
	st, _ := liveStore(t)
	ctx := context.Background()
	old := st.B.(*Redis)
	op := OpRecord{ID: "old-lock", Lock: true, At: time.Now()}
	held, err := old.Lock(ctx, op)
	require.NoError(t, err)
	require.True(t, held)
	advanced, err := old.AdvanceEpoch(ctx, 0, time.Now())
	require.NoError(t, err)
	require.True(t, advanced)
	fresh := old.AtEpoch(1, false).(*Redis)
	next := OpRecord{ID: "new-lock", Lock: true, At: time.Now()}
	held, err = fresh.Lock(ctx, next)
	require.NoError(t, err)
	require.True(t, held)
	require.NoError(t, old.Release(ctx, op, false))
	f, err := fresh.ReadFence(ctx)
	require.NoError(t, err)
	require.NotNil(t, f.Pending)
	require.Equal(t, next.ID, f.Pending.ID)
	require.Equal(t, uint64(1), f.Gen)
	require.NoError(t, fresh.Release(ctx, next, false))
}
