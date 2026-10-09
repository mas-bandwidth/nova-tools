//go:build linux && functional

package pg

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const watchResistantEnv = "NOVA_TEST_PG_WATCH_RESISTANT"

func init() {
	if os.Getenv(watchResistantEnv) != "1" {
		return
	}
	signal.Ignore(syscall.SIGTERM)
	if _, err := fmt.Fprintln(os.Stdout, "resistant ready"); err != nil {
		os.Exit(2)
	}
	select {}
}

// A stopped member cannot process TERM. The watcher must stay alive through
// its own TERM, then KILL its exact private group before close reports quiet.
func TestWatchdogKillsStoppedTermResistantGroupMember(t *testing.T) {
	t.Parallel()
	w, err := startWatchdog()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, w.close(), "watcher cleanup") })
	exe, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := subproc.Long(ctx, exe, "-test.run=^$")
	cmd.Env = []string{watchResistantEnv + "=1"}
	joinWatchdog(cmd, w.group)
	out, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	memberDone := false
	t.Cleanup(func() {
		if !memberDone {
			if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				assert.NoError(t, err, "owned child cleanup")
			}
			select {
			case <-waited:
			case <-time.After(15 * time.Second):
				assert.Fail(t, "owned child did not join during cleanup")
			}
		}
	})
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(out).ReadString('\n')
		ready <- line
	}()
	select {
	case line := <-ready:
		require.Equal(t, "resistant ready\n", line)
	case <-time.After(15 * time.Second):
		require.FailNow(t, "test group member did not become ready")
	}
	require.NoError(t, cmd.Process.Signal(syscall.SIGSTOP))
	require.Eventually(t, func() bool {
		body, err := os.ReadFile(filepath.Join("/proc", fmt.Sprint(cmd.Process.Pid), "stat"))
		if err != nil {
			return false
		}
		end := strings.LastIndexByte(string(body), ')')
		return end >= 0 && strings.HasPrefix(strings.TrimSpace(string(body[end+1:])), "T ")
	}, 15*time.Second, 10*time.Millisecond, "owned group member never stopped")
	require.True(t, groupRunning(w.group), "fixture did not join the watcher's group")
	require.NoError(t, w.close(), "watcher must prove the group quiet after escalation")
	select {
	case err := <-waited:
		memberDone = true
		var exit *exec.ExitError
		require.True(t, errors.As(err, &exit), "member exit: %v", err)
		status, ok := exit.ProcessState.Sys().(syscall.WaitStatus)
		require.True(t, ok)
		require.True(t, status.Signaled())
		require.Equal(t, syscall.SIGKILL, status.Signal())
	case <-time.After(15 * time.Second):
		require.FailNow(t, "stopped member survived watcher cleanup")
	}
	require.False(t, groupRunning(w.group), "the watcher's owned group remains runnable")
}
