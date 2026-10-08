//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/procgroup"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func anchoredLeaderlessGroup(t *testing.T) (string, int, string, []string) {
	t.Helper()
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	cmd, release, abort, err := nativeGroupCommand(context.Background(), "/bin/sh", "-c", `(trap '' TERM; echo ready > "$1"; while :; do sleep 1; done) & exit 0`, "sh", ready)
	require.NoError(t, err)
	t.Cleanup(abort)
	require.NoError(t, cmd.Start())
	pid, birth := cmd.Process.Pid, swarm.StartStamp(cmd.Process.Pid)
	require.NoError(t, writeNativeGroupReceipt(dir, pid, birth))
	require.NoError(t, startNativeAnchor(dir, pid))
	t.Cleanup(func() {
		if groupRunnable(pid) {
			_ = syscall.Kill(-pid, syscall.SIGKILL) // ignored: this test owns the group
		}
	})
	require.NoError(t, release())
	require.NoError(t, cmd.Wait())
	require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, time.Second, 10*time.Millisecond)
	assert.NotEqual(t, birth, swarm.StartStamp(pid))
	receipt, err := os.ReadFile(filepath.Join(dir, nativeAnchorReceiptName))
	require.NoError(t, err)
	fields := strings.Fields(string(receipt))
	require.Len(t, fields, 5)
	return dir, pid, birth, fields
}

func TestAnchorRefusesReplacedChannelAndKeepsStopDebt(t *testing.T) {
	t.Parallel()
	dir, pid, birth, fields := anchoredLeaderlessGroup(t)
	fifo := filepath.Join(dir, fields[2])
	require.NoError(t, os.Rename(fifo, fifo+".old"))
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))
	reader, err := os.OpenFile(fifo, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()
	c := &nativeChild{groupDir: dir, done: make(chan struct{})}
	close(c.done)
	c.stopGroupAfterGrace()
	assert.False(t, c.StopConfirmed(), "replacement channel cannot authorize STOP return")
	assert.True(t, groupRunnable(pid), "the owner must not signal through a replacement channel")
	assert.False(t, procgroup.KillVerified(context.Background(), pid, birth, filepath.Join(dir, nativeAnchorReceiptName), time.Second))
	assert.True(t, groupRunnable(pid))
}

func TestAnchorRefusesWrongBirthBeforeCommand(t *testing.T) {
	t.Parallel()
	dir, pid, birth, fields := anchoredLeaderlessGroup(t)
	fields[1] = "wrong-birth"
	receipt := filepath.Join(dir, nativeAnchorReceiptName)
	require.NoError(t, os.WriteFile(receipt, []byte(strings.Join(fields, " ")+"\n"), 0o600))
	assert.False(t, procgroup.KillVerified(context.Background(), pid, birth, receipt, time.Second))
	assert.True(t, groupRunnable(pid), "a reused anchor PID must not receive a group command")
}

func TestAnchorDeathAfterTermCommandKeepsStopDebt(t *testing.T) {
	t.Parallel()
	dir, pid, _, fields := anchoredLeaderlessGroup(t)
	fifo := filepath.Join(dir, fields[2])
	writer, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	require.NoError(t, err)
	_, err = writer.Write([]byte("TERM\n"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	anchorPID, err := strconv.Atoi(fields[0])
	require.NoError(t, err)
	require.NoError(t, syscall.Kill(anchorPID, syscall.SIGKILL))
	require.Eventually(t, func() bool { return swarm.StartStamp(anchorPID) == "-" }, time.Second, 10*time.Millisecond)
	c := &nativeChild{groupDir: dir, done: make(chan struct{})}
	close(c.done)
	c.stopGroupAfterGrace()
	assert.False(t, c.StopConfirmed(), "anchor death after TERM must retain debt")
	assert.True(t, groupRunnable(pid), "the TERM-resistant child must remain owed")
}
