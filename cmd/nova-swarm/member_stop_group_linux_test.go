//go:build linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupGateRecordsIdentityBeforeHarnessExec(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	cmd, release, abort, err := nativeGroupCommand(context.Background(), "/bin/sh", "-c", "echo started > \"$1\"", "sh", marker)
	require.NoError(t, err)
	defer abort()
	require.NoError(t, cmd.Start())
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	_, statErr := os.Stat(marker)
	assert.True(t, os.IsNotExist(statErr), "harness ran before the receipt")
	require.NoError(t, writeNativeGroupReceipt(dir, cmd.Process.Pid, swarm.StartStamp(cmd.Process.Pid)))
	require.NoError(t, release())
	require.Eventually(t, func() bool { _, err := os.Stat(marker); return err == nil }, time.Second, 10*time.Millisecond)
}

func TestRecoveredStopReapsResistantOwnedGroup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	cmd, release, abort, err := nativeGroupCommand(context.Background(), "/bin/sh", "-c", "trap '' TERM; echo ready > \"$1\"; while :; do sleep 1; done", "sh", ready)
	require.NoError(t, err)
	defer abort()
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	defer func() { _ = syscall.Kill(-pid, syscall.SIGKILL); _ = cmd.Wait() }()
	require.NoError(t, writeNativeGroupReceipt(dir, pid, swarm.StartStamp(pid)))
	require.NoError(t, release())
	require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, time.Second, 10*time.Millisecond)
	c := &nativeChild{groupDir: dir, done: make(chan struct{})}
	close(c.done) // previous member and native parent are gone
	started := time.Now()
	c.Stop()
	require.Eventually(t, c.StopConfirmed, 10*time.Second, 25*time.Millisecond)
	assert.GreaterOrEqual(t, time.Since(started), swarm.TerminateGrace, "the resistant group should need escalation")
	assert.False(t, groupRunnable(pid), "the resistant harness group survived STOP")
}

func TestStopWillNotSignalReusedGroupNumber(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	cmd, release, abort, err := nativeGroupCommand(context.Background(), "/bin/sh", "-c", "echo ready > \"$1\"; sleep 30", "sh", ready)
	require.NoError(t, err)
	defer abort()
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	defer func() { _ = syscall.Kill(-pid, syscall.SIGKILL); _ = cmd.Wait() }()
	require.NoError(t, writeNativeGroupReceipt(dir, pid, "wrong-birth"))
	require.NoError(t, release())
	require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, time.Second, 10*time.Millisecond)
	c := &nativeChild{groupDir: dir, done: make(chan struct{})}
	close(c.done)
	c.Stop()
	time.Sleep(100 * time.Millisecond)
	assert.True(t, swarm.GroupAlive(pid, ""), "a mismatched identity killed an unrelated group")
	assert.False(t, c.StopConfirmed())
}
