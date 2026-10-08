//go:build darwin

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

func TestDarwinBirthIdentityAndRecoveredResistantGroupStop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	cmd, release, abort, err := nativeGroupCommand(context.Background(), "/bin/sh", "-c", "trap '' TERM; echo ready > \"$1\"; while :; do sleep 1; done", "sh", ready)
	require.NoError(t, err)
	defer abort()
	require.NoError(t, cmd.Start())
	pid, stamp := cmd.Process.Pid, swarm.StartStamp(cmd.Process.Pid)
	require.NotEqual(t, "-", stamp)
	waited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(waited) }()
	defer func() {
		if swarm.StartStamp(pid) == stamp {
			swarm.KillGroup(pid, stamp)
		}
		<-waited
	}()
	require.NoError(t, writeNativeGroupReceipt(dir, pid, stamp))
	require.NoError(t, release())
	require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, time.Second, 10*time.Millisecond)
	c := &nativeChild{groupDir: dir, done: make(chan struct{})}
	close(c.done) // the previous member/native parent is unavailable
	c.Stop()
	require.Eventually(t, c.StopConfirmed, 10*time.Second, 25*time.Millisecond)
	assert.False(t, groupRunnable(pid))
}

func TestDarwinRecoveredStopReapsLeaderlessResistantGroup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	cmd, release, abort, err := nativeGroupCommand(context.Background(), "/bin/sh", "-c", `(trap '' TERM; echo ready > "$1"; while :; do sleep 1; done) & exit 0`, "sh", ready)
	require.NoError(t, err)
	defer abort()
	require.NoError(t, cmd.Start())
	pid, stamp := cmd.Process.Pid, swarm.StartStamp(cmd.Process.Pid)
	require.NotEqual(t, "-", stamp)
	require.NoError(t, writeNativeGroupReceipt(dir, pid, stamp))
	require.NoError(t, startNativeAnchor(dir, pid))
	defer func() {
		if anchorInGroup(dir, pid) {
			swarm.KillGroup(pid, stamp)
		}
	}()
	require.NoError(t, release())
	require.NoError(t, cmd.Wait())
	require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, time.Second, 10*time.Millisecond)
	assert.NotEqual(t, stamp, swarm.StartStamp(pid))
	assert.True(t, anchorInGroup(dir, pid))

	other, otherRelease, otherAbort, err := nativeGroupCommand(context.Background(), "/bin/sh", "-c", "sleep 30")
	require.NoError(t, err)
	defer otherAbort()
	require.NoError(t, other.Start())
	defer func() { _ = other.Process.Kill(); _ = other.Wait() }()
	require.NoError(t, otherRelease())

	c := &nativeChild{groupDir: dir, done: make(chan struct{})}
	close(c.done)
	c.Stop()
	require.Eventually(t, c.StopConfirmed, 10*time.Second, 25*time.Millisecond)
	assert.False(t, groupRunnable(pid))
	assert.True(t, processAlive(other.Process.Pid), "an unrelated group was signalled")
}

func TestDarwinRecoveredStopKeepsDebtIfAnchorIsGone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	cmd, release, abort, err := nativeGroupCommand(context.Background(), "/bin/sh", "-c", `(trap '' TERM; echo ready > "$1"; while :; do sleep 1; done) & exit 0`, "sh", ready)
	require.NoError(t, err)
	defer abort()
	require.NoError(t, cmd.Start())
	pid, stamp := cmd.Process.Pid, swarm.StartStamp(cmd.Process.Pid)
	require.NotEqual(t, "-", stamp)
	require.NoError(t, writeNativeGroupReceipt(dir, pid, stamp))
	require.NoError(t, release())
	require.NoError(t, cmd.Wait())
	defer func() { _ = syscall.Kill(-pid, syscall.SIGKILL) }()
	require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, time.Second, 10*time.Millisecond)
	assert.NotEqual(t, stamp, swarm.StartStamp(pid))
	c := &nativeChild{groupDir: dir, done: make(chan struct{})}
	close(c.done)
	c.stopGroupAfterGrace()
	assert.False(t, c.StopConfirmed())
	assert.True(t, groupRunnable(pid), "unverified group must not be signalled")
}
