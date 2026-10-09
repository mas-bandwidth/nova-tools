//go:build linux || darwin

package friend

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolResultNativeWatchSeesPublicationBeforeBlocking(t *testing.T) {
	t.Parallel()
	r := toolResultRig(t)
	watch, err := newToolResultWatch(r.Dir)
	require.NoError(t, err)
	defer watch.Close() // ignored: test observer only
	// Publish after watch installation but before blocking: kernel retains
	// the edge even when producer and receiver scheduling cross.
	require.NoError(t, toolResultWrite(filepath.Join(r.Dir, "retained.pending.json"), map[string]string{"fixture": "untrusted"}))
	changed, err := watch.Wait(context.Background(), 20*time.Second)
	require.NoError(t, err)
	assert.True(t, changed)
}

func TestToolResultNativeWaitWakesForActualProducerAndReplaysOnRearm(t *testing.T) {
	t.Parallel()
	r := toolResultRig(t)
	observer, err := newToolResultWatch(r.Dir)
	require.NoError(t, err)
	defer observer.Close() // ignored: test observer only
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		event *ToolResultEvent
		err   error
	}
	done := make(chan result, 1)
	go func() {
		event, err := r.Wait(ctx, 20*time.Second)
		done <- result{event, err}
	}()
	// Observe Arm through the native watcher, with no sleeps or polling.
	for {
		var binding toolResultBinding
		ok, err := toolResultRead(filepath.Join(r.Dir, "binding.json"), &binding)
		require.NoError(t, err)
		if ok {
			break
		}
		changed, err := observer.Wait(ctx, 20*time.Second)
		require.NoError(t, err)
		require.True(t, changed)
	}
	_, err = r.Deliver(ctx, "fresh actual producer publication")
	require.NoError(t, err)
	got := <-done
	require.NoError(t, got.err)
	require.NotNil(t, got.event)
	assert.Equal(t, "fresh actual producer publication", got.event.Text)
	// Stopping/canceling observation never acknowledges the payload.
	require.NoError(t, r.Release())
	require.NoError(t, r.Retain(context.Background(), "published during lease gap"))
	cancel()
	_, err = r.Wait(ctx, 0)
	require.ErrorIs(t, err, context.Canceled)
	again, err := r.Wait(context.Background(), 0)
	require.NoError(t, err)
	require.Equal(t, got.event, again)
	require.NoError(t, r.Ack(again.ID))
	pending, err := r.Wait(context.Background(), 0)
	require.NoError(t, err)
	require.NotNil(t, pending)
	assert.Equal(t, "published during lease gap", pending.Text)
}

func TestToolResultNativeCancellationAndDirectoryInvalidationPreserveFiles(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"cancel", "move"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			r := toolResultRig(t)
			_, err := r.Wait(context.Background(), 0)
			require.NoError(t, err)
			require.NoError(t, r.Release())
			require.NoError(t, r.Retain(context.Background(), "retained across observer failure"))
			watch, err := newToolResultWatch(r.Dir)
			require.NoError(t, err)
			defer watch.Close() // ignored: test observer only
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := watch.Wait(ctx, 20*time.Second)
				done <- err
			}()
			if kind == "cancel" {
				cancel()
				require.ErrorIs(t, <-done, context.Canceled)
			} else {
				newDir := filepath.Join(t.TempDir(), "moved-owned-directory")
				require.NoError(t, os.Rename(r.Dir, newDir))
				r.Dir = newDir
				require.ErrorContains(t, <-done, "pending events preserved")
			}
			event, err := r.Wait(context.Background(), 0)
			require.NoError(t, err)
			require.NotNil(t, event)
			assert.Equal(t, "retained across observer failure", event.Text)
			require.NoFileExists(t, filepath.Join(r.Dir, event.ID+".receipt.json"))
		})
	}
}
