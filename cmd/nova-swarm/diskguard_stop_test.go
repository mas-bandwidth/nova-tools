package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The stop floor uses a fake signal. No test calls the operating system's signal.

func TestDiskGuardStopFloorSignalsALockAndWritesTheMarker(t *testing.T) {
	t.Parallel()
	g, out := dgGuard(t)
	dir := t.TempDir()
	g.stopFloor = 50 * gib
	g.marker = filepath.Join(dir, "disk")
	g.runDir = dir
	g.self = 99
	g.free = func(string) (uint64, error) { return 10 * gib, nil }
	var signaled []int
	g.alive = func(pid int) bool { return pid == 42 }
	g.signal = func(pid int) error {
		signaled = append(signaled, pid)
		return nil
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.lock"), []byte("42\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "self.lock"), []byte("99\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dead.lock"), []byte("7\n"), 0o644))
	assert.Equal(t, 3, g.run())
	assert.Equal(t, []int{42}, signaled)
	raw, err := os.ReadFile(g.marker)
	require.NoError(t, err)
	assert.Equal(t, "free_gib=10\nstop=1\n", string(raw))
	assert.Contains(t, out.String(), "DISK-GUARD STOP pid=42")
	assert.Contains(t, out.String(), "DISK-GUARD STOP freed=")
}

func TestDiskGuardStopFloorDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	g, out := dgGuard(t)
	dir := t.TempDir()
	g.stopFloor = 50 * gib
	g.marker = filepath.Join(dir, "disk")
	g.runDir = dir
	g.dry = true
	g.free = func(string) (uint64, error) { return 1 * gib, nil }
	g.alive = func(int) bool { return true }
	g.signal = func(int) error { t.Fatal("signaled"); return nil }
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.lock"), []byte("42\n"), 0o644))
	assert.Equal(t, 0, g.run())
	_, err := os.Stat(g.marker)
	assert.True(t, os.IsNotExist(err))
	assert.Contains(t, out.String(), "WOULD-STOP pid=42")
	assert.Contains(t, out.String(), "DISK-GUARD OK")
}

func TestDiskGuardAboveTheStopFloorClearsTheMarker(t *testing.T) {
	t.Parallel()
	g, _ := dgGuard(t)
	dir := t.TempDir()
	g.stopFloor = 5 * gib
	g.marker = filepath.Join(dir, "disk")
	g.free = func(string) (uint64, error) { return 100 * gib, nil }
	g.signal = func(int) error { t.Fatal("signaled"); return nil }
	assert.Equal(t, 0, g.run())
	raw, err := os.ReadFile(g.marker)
	require.NoError(t, err)
	assert.Equal(t, "free_gib=100\nstop=0\n", string(raw))
}

func TestDiskGuardStopFloorUnreadHomeDoesNotStop(t *testing.T) {
	t.Parallel()
	g, out := dgGuard(t)
	g.stopFloor = 1 * gib
	g.free = func(string) (uint64, error) { return 0, os.ErrNotExist }
	g.signal = func(int) error { t.Fatal("signaled"); return nil }
	assert.Equal(t, 0, g.run())
	assert.NotContains(t, out.String(), "STOP")
}

func TestDiskGuardRefusesANegativeStopFloorAndAMissingMarkerDir(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	assert.Equal(t, 2, cmdDiskGuard([]string{"--stop-floor", "-1"}, &stdout, &stderr))
	assert.Contains(t, stderr.String(), "--stop-floor")
	stdout.Reset()
	stderr.Reset()
	if runtime.GOOS == "windows" {
		return
	}
	assert.Equal(t, 2, cmdDiskGuard([]string{"--marker", filepath.Join(t.TempDir(), "missing", "disk")}, &stdout, &stderr))
	assert.Contains(t, stderr.String(), "--marker")
	assert.Empty(t, stdout.String())
}
