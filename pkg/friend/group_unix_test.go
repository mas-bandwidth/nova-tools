//go:build unix && functional

package friend

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A deliver command that forks (as opencode run does) and is stopped (the
// daemon cancels a turn silent past its SilentStop) is stopped with
// everything it forked: the grandchild's group is gone once Deliver returns. Both tests fork a real /bin/sh and run on
// real time, so they are the functional tier's, not the unit tier's.
func TestAStoppedDeliveryIsStoppedWithItsWholeProcessGroup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild.pid")
	// the child forks a grandchild that ignores SIGTERM, writes its pid, and both sleep
	// it prints once the grandchild is up, and that print is the stop: no clock
	script := "trap '' TERM; sleep 60 & echo $! > " + pidFile + "; echo up; wait"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = WithOutputSeen(ctx, cancel)
	_, exit, err := realExec(ctx, 200*time.Millisecond, dir, "/bin/sh", []string{"-c", script}, "")
	require.ErrorContains(t, err, "the delivery was stopped with its process group")
	assert.NotEqual(t, 0, exit)
	raw, err := os.ReadFile(pidFile)
	require.NoError(t, err, "the grandchild had started")
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	require.NoError(t, err)
	assert.Eventually(t, func() bool { return !GroupAlive(pid) && !processAlive(pid) }, 5*time.Second, 50*time.Millisecond, "the grandchild (pid %d) outlived the delivery", pid)
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(nil) == nil // a zombie or a dead process refuses
}

func TestADeliveryWithinTheBudgetAnswersTheExitCode(t *testing.T) {
	t.Parallel()
	out, exit, err := realExec(context.Background(), time.Second, t.TempDir(), "/bin/sh", []string{"-c", "cat; exit 3"}, "in\n")
	require.NoError(t, err)
	assert.Equal(t, 3, exit)
	assert.Equal(t, "in\n", out, "stdin reaches the command")
}

// A harness says why it refused on stderr (dsh does), so a nonzero exit's
// output carries stderr after stdout; a turn that answered keeps stdout only.
func TestAFailedDeliveryCarriesStderrInItsOutput(t *testing.T) {
	t.Parallel()
	out, exit, err := realExec(context.Background(), time.Second, t.TempDir(), "/bin/sh", []string{"-c", "echo said; echo why >&2; exit 1"}, "")
	require.NoError(t, err)
	assert.Equal(t, 1, exit)
	assert.Equal(t, "said\nwhy\n", out)
	out, exit, err = realExec(context.Background(), time.Second, t.TempDir(), "/bin/sh", []string{"-c", "echo said; echo noise >&2"}, "")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	assert.Equal(t, "said\n", out)
}

// With no text for stdin the command reads /dev/null, never a pipe held
// open: a headless opencode run with stdin left open hangs at init for ever
// (measured 2026-10-04). cat ends at once on it.
func TestADeliveryWithNoStdinReadsDevNull(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, exit, err := realExec(ctx, time.Second, t.TempDir(), "/bin/sh", []string{"-c", "cat; ls -l /dev/fd/0 >/dev/null 2>&1; echo done"}, "")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	assert.Equal(t, "done\n", out)
}

// A command that prints is said to the watch as it prints.
func TestADeliverysOutputIsSaidToTheWatch(t *testing.T) {
	t.Parallel()
	var n atomic.Int64
	ctx := WithOutputSeen(context.Background(), func() { n.Add(1) })
	_, _, err := realExec(ctx, time.Second, t.TempDir(), "/bin/sh", []string{"-c", "echo a; echo b >&2"}, "")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n.Load(), int64(2))
}

// GroupAlive says whether any process of the group led by pid is still there (a signal of 0
// to the group), for the test of the kill.
func GroupAlive(pid int) bool { return syscall.Kill(-pid, 0) == nil }
