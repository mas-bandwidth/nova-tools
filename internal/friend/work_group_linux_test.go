//go:build linux

package friend

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/procgroup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Losing the anchor cannot turn a live group's cleanup failure into an
// ordinary harness exit (docs/SPEC-FRIEND.md, native group exit proof).
func TestRealExecKeepsGroupCleanupFailureWithNonzeroExit(t *testing.T) {
	t.Parallel()
	dir, job := t.TempDir(), "card~1"
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "jobs", job), 0o755))
	anchorPath := runReceiptPath(dir, job) + ".anchor"
	var anchor []byte
	var anchorErr error
	var lost sync.Once
	pid := 0
	ctx := WithProcessStarted(context.Background(), func(started int) error {
		pid = started
		return writeRunReceipt(dir, job, runReceipt{RunID: "run-1", PID: pid, Identity: ProcessIdentity(pid)})
	})
	ctx = WithProcessGroupAnchor(ctx, anchorPath)
	ctx = WithOutputSeen(ctx, func() {
		lost.Do(func() {
			anchor, anchorErr = os.ReadFile(anchorPath)
			if anchorErr == nil {
				anchorErr = os.Remove(anchorPath)
			}
		})
	})
	defer func() {
		if len(anchor) > 0 {
			assert.NoError(t, os.WriteFile(anchorPath, anchor, 0o600))
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			assert.True(t, procgroup.ReapVerified(cleanupCtx, pid, "", anchorPath, 100*time.Millisecond))
		}
	}()
	out, exit, err := realExec(ctx, 100*time.Millisecond, dir, "/bin/sh", []string{"-c", "sleep 30 >/dev/null 2>&1 & echo ready; echo refused >&2; exit 7"}, "")
	require.NoError(t, anchorErr)
	assert.True(t, procgroup.GroupRunnable(pid), "lost authority must leave live descendants unverified")
	assert.Equal(t, 7, exit)
	assert.Contains(t, out, "ready")
	assert.Contains(t, out, "refused")
	assert.ErrorContains(t, err, "native process group remains live")
}
