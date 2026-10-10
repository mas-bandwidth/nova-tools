//go:build unix

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTheLandingGateDiesWithTheServer(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := newApp(os.Getenv)
	a.landRoot = func() (string, error) { return dir, nil }
	gate := exec.Command("sleep", "60")
	gate.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, gate.Start())
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gate_pid"), []byte(strconv.Itoa(gate.Process.Pid)+"\n"), 0o600))
	t.Cleanup(func() {
		_ = syscall.Kill(-gate.Process.Pid, syscall.SIGKILL)
		_ = gate.Wait()
	})

	ctx, cancel := context.WithCancel(context.Background())
	a.landServerCancel = cancel
	require.NoError(t, ctx.Err())
	require.NoError(t, a.stopLandingServer())
	assert.ErrorIs(t, ctx.Err(), context.Canceled, "server shutdown cancels the landing subprocess context")
	require.Error(t, gate.Wait(), "server shutdown killed the gate")
	assert.Error(t, gate.Process.Signal(syscall.Signal(0)), "server shutdown ends the recorded gate process group")
}
