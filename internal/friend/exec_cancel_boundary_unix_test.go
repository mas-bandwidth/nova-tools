//go:build linux || darwin

package friend

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A started leader is not group-exit proof, even when cancellation or a refused
// launch callback reaches realExec's early return (NativeControlAuthority).
func TestStartedUnreceiptedEarlyRefusalKeepsGroupUnverifiedWarning(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"cancel-after-start", "refuse-after-start", "cancel-before-start"} {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cause := errors.New("synthetic launch refusal")
			pid := 0
			ctx = WithProcessStarted(ctx, func(started int) error {
				pid = started
				if reason == "refuse-after-start" {
					return cause
				}
				cancel()
				return nil
			})
			if reason == "cancel-before-start" {
				cancel()
			}
			dir := t.TempDir()
			_, _, err := RealExec(ctx, dir, "/bin/sh", []string{"-c", "echo body > body-started"}, "")
			require.Error(t, err)
			assert.NoFileExists(t, filepath.Join(dir, "body-started"), "a refused native gate never executes the body")
			if reason == "cancel-before-start" {
				assert.Zero(t, pid, "Start failure must not invent a native child")
				assert.ErrorIs(t, err, context.Canceled)
				assert.NotContains(t, err.Error(), "unreceipted descendants", "no-child failure does not invent group debt")
				return
			}
			assert.Positive(t, pid, "the deterministic early boundary has a real native leader")
			assert.Empty(t, ProcessIdentity(pid), "the owner waited for this leader's exit")
			assert.ErrorContains(t, err, "unreceipted descendants are unverified")
			assert.NotContains(t, err.Error(), "verified process group")
			if reason == "refuse-after-start" {
				assert.ErrorIs(t, err, cause)
			} else {
				assert.ErrorIs(t, err, context.Canceled)
			}
		})
	}
}
