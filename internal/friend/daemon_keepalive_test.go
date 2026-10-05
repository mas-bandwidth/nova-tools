package friend

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeepaliveStartsAfterSingletonAndJoinsDaemonShutdown(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		r.d.StateDir = t.TempDir()
		started, joined := false, false
		r.d.Keepalive = func(ctx context.Context) error { started = true; <-ctx.Done(); joined = true; return nil }
		now := r.d.Now
		r.d.Now = func() time.Time { synctest.Wait(); return now() }
		r.run(t, 4)
		assert.True(t, started)
		assert.True(t, joined, "daemon return joins its keepalive child")
	})
}

func TestSecondDaemonCannotStartKeepaliveBeforeSingletonRefusal(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.StateDir = t.TempDir()
	lock, err := TakeDaemonLock(r.d.StateDir, r.d.Friend)
	require.NoError(t, err)
	defer func() { assert.NoError(t, lock.Unlock()) }()
	started := false
	r.d.Keepalive = func(context.Context) error { started = true; return nil }
	require.Error(t, r.d.Run(context.Background()))
	assert.False(t, started)
}

func TestFatalKeepaliveExitStopsTheDaemonAndSurfacesTheError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		r.d.Keepalive = func(context.Context) error { return errors.New("keepalive failed") }
		now := r.d.Now
		r.d.Now = func() time.Time { synctest.Wait(); return now() }
		err := r.d.Run(context.Background())
		require.ErrorContains(t, err, "keepalive failed")
		assert.Empty(t, r.delivered)
	})
}
