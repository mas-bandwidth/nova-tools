//go:build unix && functional

package friend

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A deliver command that forks (as opencode run does) and runs past the
// budget is stopped with everything it forked: the grandchild's group is
// gone once Deliver returns. The budget here is the test's, not the wall
// clock's bound on the tool. Both tests fork a real /bin/sh and run on
// real time, so they are the functional tier's, not the unit tier's.
func TestADeliveryPastTheBudgetIsStoppedWithItsWholeProcessGroup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild.pid")
	// the child forks a grandchild that ignores SIGTERM, writes its pid, and both sleep
	script := "trap '' TERM; sleep 60 & echo $! > " + pidFile + "; wait"
	_, exit, err := realExec(context.Background(), 300*time.Millisecond, 200*time.Millisecond, dir, "/bin/sh", []string{"-c", script}, "")
	require.ErrorContains(t, err, "ran past 300ms and was stopped with its process group")
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
	out, exit, err := realExec(context.Background(), 10*time.Second, time.Second, t.TempDir(), "/bin/sh", []string{"-c", "cat; exit 3"}, "in\n")
	require.NoError(t, err)
	assert.Equal(t, 3, exit)
	assert.Equal(t, "in\n", out, "stdin reaches the command")
}

// A harness says why it refused on stderr (dsh does), so a nonzero exit's
// output carries stderr after stdout; a turn that answered keeps stdout only.
func TestAFailedDeliveryCarriesStderrInItsOutput(t *testing.T) {
	t.Parallel()
	out, exit, err := realExec(context.Background(), 10*time.Second, time.Second, t.TempDir(), "/bin/sh", []string{"-c", "echo said; echo why >&2; exit 1"}, "")
	require.NoError(t, err)
	assert.Equal(t, 1, exit)
	assert.Equal(t, "said\nwhy\n", out)
	out, exit, err = realExec(context.Background(), 10*time.Second, time.Second, t.TempDir(), "/bin/sh", []string{"-c", "echo said; echo noise >&2"}, "")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	assert.Equal(t, "said\n", out)
}
