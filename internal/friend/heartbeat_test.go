//go:build functional

package friend

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The heartbeat is independent of a session/store task blocked for ten
// seconds, and sends from startup until cancellation (tla/Presence.tla).
func TestHeartbeatKeepsItsCadenceWhileSessionWorkIsBlocked(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		blocked := make(chan struct{})
		go func() { <-ctx.Done(); close(blocked) }()
		var beats []time.Time
		done := make(chan struct{})
		go func() {
			defer close(done)
			require.NoError(t, Heartbeat(ctx, func(context.Context) error {
				beats = append(beats, time.Now())
				return nil
			}))
		}()
		synctest.Wait()
		first := beats[0]
		for i := 1; i <= 10; i++ {
			time.Sleep(BeatEvery)
			synctest.Wait()
			require.Len(t, beats, i+1)
			assert.Equal(t, first.Add(time.Duration(i)*BeatEvery), beats[i])
		}
		cancel()
		synctest.Wait()
		<-done
		<-blocked
	})
}

// A stalled sender gets one bounded attempt each second; attempts never
// overlap and transport failure never fabricates session evidence.
func TestHeartbeatBoundsEachAttemptAndContinuesAfterFailure(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var starts []time.Time
		active, maximum := 0, 0
		done := make(chan struct{})
		go func() {
			defer close(done)
			require.NoError(t, Heartbeat(ctx, func(call context.Context) error {
				starts = append(starts, time.Now())
				active++
				maximum = max(maximum, active)
				<-call.Done()
				active--
				return call.Err()
			}))
		}()
		synctest.Wait()
		for range 5 {
			time.Sleep(BeatEvery)
			synctest.Wait()
		}
		cancel()
		synctest.Wait()
		<-done
		require.Len(t, starts, 6)
		assert.Equal(t, 1, maximum)
		for i := 1; i < len(starts); i++ {
			assert.Equal(t, BeatEvery, starts[i].Sub(starts[i-1]))
		}
	})
}
