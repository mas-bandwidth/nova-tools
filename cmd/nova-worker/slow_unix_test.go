//go:build unix && slow

// The test here waits a process group out, so it sits behind `slow` beside slow_test.go.

package main

import (
	"io"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestAToolTimeoutReapsOnlyTheWrapperGroup pins the shell shim's group-reaping wrapper:
// when a tool timeout signals the wrapper pid, the wrapper must carry its own process
// group -- the real shell and everything it started -- and not only the pid the timeout
// hit, while leaving a bystander in a different session untouched (SPEC-WORKER.md rule 9,
// "A job is one blocking process group, reported once"; nova-tools #1814, the shell
// wrapper).
func TestAToolTimeoutReapsOnlyTheWrapperGroup(t *testing.T) {
	t.Parallel()
	dir, _, err := writeNativeShellShims(t.TempDir())
	require.NoError(t, err)
	shim := filepath.Join(dir, "sh")
	marker := filepath.Join(t.TempDir(), "sleeper.pid")
	// The sleeper ignores TERM. Only a group signal stops it. The shell stays
	// the parent so today's exec of the real shell is the pid the timeout hits.
	script := "trap '' TERM INT; sleep 30 & echo $! > " + shQuote(marker) + "; wait"
	cmd := exec.Command(shim, "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		signalGroup(cmd.Process.Pid)
		_ = cmd.Wait()
	})

	bystander := exec.Command("/bin/sh", "-c", "sleep 30")
	bystander.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	require.NoError(t, bystander.Start())
	t.Cleanup(func() {
		signalGroup(bystander.Process.Pid)
		_ = bystander.Wait()
	})

	var sleeper int
	require.Eventually(t, func() bool {
		sleeper = readPID(marker)
		return sleeper > 0 && alive(sleeper)
	}, 5*time.Second, 20*time.Millisecond)

	require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))
	require.Eventually(t, func() bool { return !alive(sleeper) }, 5*time.Second, 20*time.Millisecond)
	require.True(t, alive(bystander.Process.Pid), "a group this launch did not start was signaled")
}
