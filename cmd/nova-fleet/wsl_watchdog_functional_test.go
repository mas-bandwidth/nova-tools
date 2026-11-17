//go:build functional

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFunctionalWslWatch compiles NovaWslWatch from roles/github-runner/files/wsl-watchdog.ps1 at C# 5 with warnings as
// errors, runs its state transitions, bounded process timeout, log rotation and SSH banner probe, and holds the emitted
// ticks equal to tla/WslWatchLog.tla (checked against WslWatch.tla by WslWatchTrace.tla; WSLWATCH_UPDATE_LOG=1 rewrites
// the log).
func TestFunctionalWslWatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gotPath := filepath.Join(dir, "WslWatchLog.tla")
	xsdPath, err := filepath.Abs("testdata/wslwatch/task.xsd")
	require.NoError(t, err)
	kaScript, err := filepath.Abs("roles/github-runner/files/runner-windows.ps1")
	require.NoError(t, err)
	wdScript, err := filepath.Abs("roles/github-runner/files/wsl-watchdog.ps1")
	require.NoError(t, err)
	runCSharp(t, "testdata/wslwatch/Program.cs",
		[]string{"roles/github-runner/files/wsl-watchdog.ps1"}, dir, gotPath, xsdPath, kaScript, wdScript)

	got, err := os.ReadFile(gotPath)
	require.NoError(t, err)
	logFile := filepath.Join("tla", "WslWatchLog.tla")
	if os.Getenv("WSLWATCH_UPDATE_LOG") == "1" {
		require.NoError(t, os.WriteFile(logFile, got, 0o644))
	}
	want, err := os.ReadFile(logFile)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got), "tla/WslWatchLog.tla is out of date; run with WSLWATCH_UPDATE_LOG=1")
}
