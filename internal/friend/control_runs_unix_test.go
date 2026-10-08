//go:build unix

package friend

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/procgroup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The actual control context must reap a TERM-resistant group on cancellation,
// and its durable receipt must name the group whose native exit is proven.
func TestCardlessControlContextCancelsItsWholeNativeGroup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	l := &loop{d: &Daemon{Dir: dir}, tag: "test"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = WithOutputSeen(l.controlContext(ctx, "message", 1), cancel)
	_, exit, err := RealExec(ctx, dir, "/bin/sh", []string{"-c", "trap '' TERM; sleep 30 & echo ready; wait"}, "")
	require.ErrorContains(t, err, "the delivery was stopped with its verified process group")
	assert.NotZero(t, exit)
	job := filepath.Base(filepath.Dir(processGroupAnchor(ctx)))
	r, err := readRunReceipt(dir, job)
	require.NoError(t, err)
	assert.NotEmpty(t, r.Identity)
	assert.Positive(t, r.PID)
	assert.Eventually(t, func() bool { return !procgroup.GroupRunnable(r.PID) }, 30*time.Second, 20*time.Millisecond)
}
