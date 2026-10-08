//go:build darwin

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDarwinBirthIdentityAndRecoveredResistantGroupStop(t *testing.T) {
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
