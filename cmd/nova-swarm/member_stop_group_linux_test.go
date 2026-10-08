//go:build linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	require.NoError(t, startNativeAnchor(dir, pid))
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
	c.stopGroupAfterGrace() // synchronous proof path; the mismatched birth must never signal
	assert.True(t, swarm.GroupAlive(pid, ""), "a mismatched identity killed an unrelated group")
	assert.False(t, c.StopConfirmed())
}

func TestRecoveredStopReapsLeaderlessResistantGroupWithoutTouchingAnother(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	cmd, release, abort, err := nativeGroupCommand(context.Background(), "/bin/sh", "-c", `(trap '' TERM; echo ready > "$1"; while :; do sleep 1; done) & exit 0`, "sh", ready)
	require.NoError(t, err)
	defer abort()
	require.NoError(t, cmd.Start())
	pid, stamp := cmd.Process.Pid, swarm.StartStamp(cmd.Process.Pid)
	require.NoError(t, writeNativeGroupReceipt(dir, pid, stamp))
	require.NoError(t, startNativeAnchor(dir, pid))
	defer func() {
		if anchorInGroup(dir, pid) {
			swarm.KillGroup(pid, stamp)
		}
	}()
	require.NoError(t, release())
	require.NoError(t, cmd.Wait(), "the group leader should exit on its own")
	require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, time.Second, 10*time.Millisecond)
	assert.NotEqual(t, stamp, swarm.StartStamp(pid), "the original group leader must be gone")
	assert.True(t, anchorInGroup(dir, pid), "the durable anchor must pin the leaderless group")
	anchorReceipt, err := os.ReadFile(filepath.Join(dir, nativeAnchorReceiptName))
	require.NoError(t, err)
	anchorFields := strings.Fields(string(anchorReceipt))
	require.Len(t, anchorFields, 5)

	other, otherRelease, otherAbort, err := nativeGroupCommand(context.Background(), "/bin/sh", "-c", "sleep 30")
	require.NoError(t, err)
	defer otherAbort()
	require.NoError(t, other.Start())
	defer func() { _ = other.Process.Kill(); _ = other.Wait() }()
	require.NoError(t, otherRelease())

	c := &nativeChild{groupDir: dir, done: make(chan struct{})}
	close(c.done) // member/native parent are gone; only durable receipts remain
	c.Stop()
	require.Eventually(t, c.StopConfirmed, 10*time.Second, 25*time.Millisecond)
	assert.False(t, groupRunnable(pid))
	_, channelErr := os.Lstat(filepath.Join(dir, anchorFields[2]))
	assert.True(t, os.IsNotExist(channelErr), "quiesced run retained its obsolete FIFO")
	assert.True(t, processAlive(other.Process.Pid), "an unrelated group was signalled")
}

func TestRecoveredStopKeepsDebtIfLeaderAndAnchorAreGone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	cmd, release, abort, err := nativeGroupCommand(context.Background(), "/bin/sh", "-c", `(trap '' TERM; echo ready > "$1"; while :; do sleep 1; done) & exit 0`, "sh", ready)
	require.NoError(t, err)
	defer abort()
	require.NoError(t, cmd.Start())
	pid, stamp := cmd.Process.Pid, swarm.StartStamp(cmd.Process.Pid)
	require.NoError(t, writeNativeGroupReceipt(dir, pid, stamp))
	require.NoError(t, release())
	require.NoError(t, cmd.Wait())
	defer func() { _ = syscall.Kill(-pid, syscall.SIGKILL) }()
	require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, time.Second, 10*time.Millisecond)
	assert.NotEqual(t, stamp, swarm.StartStamp(pid))
	c := &nativeChild{groupDir: dir, done: make(chan struct{})}
	close(c.done)
	c.stopGroupAfterGrace()
	assert.False(t, c.StopConfirmed(), "lost identity must retain STOP debt")
	assert.True(t, groupRunnable(pid), "unverified group must not be signalled")
}
