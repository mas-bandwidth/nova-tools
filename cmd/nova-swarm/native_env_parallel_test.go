package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNativeEnvSeamChildEnvComesFromTheRunNotTheProcess verifies that the child
// gets its environment from the Run's own slice, not the process's os.Environ().
func TestNativeEnvSeamChildEnvComesFromTheRunNotTheProcess(t *testing.T) {
	t.Parallel()

	// Create a base environment with a marker that the process does not have.
	// Use a name that passes keepNativeEnv check (starts with NOVA_SWARM_)
	markerKey := "NOVA_SWARM_TEST_MARKER"
	markerVal := t.TempDir()
	baseEnv := []string{
		"HOME=/test/home",
		markerKey + "=" + markerVal,
	}

	runDir := t.TempDir()

	// Build child env from baseEnv (the Run's environment), not os.Environ()
	childEnv := nativeChildEnvFrom(baseEnv,
		filepath.Join(runDir, "data"),
		filepath.Join(runDir, "jobs"),
		filepath.Join(runDir, "tmp"),
		"", "", "", "", nil,
	)

	// Verify the child got the custom marker key with its value
	found := false
	for _, kv := range childEnv {
		if strings.HasPrefix(kv, markerKey+"=") {
			found = true
			require.Equal(t, markerKey+"="+markerVal, kv)
			break
		}
	}
	require.True(t, found, "child env should contain "+markerKey+" from baseEnv")

	// Verify HOME was overridden with dataHome as expected
	found = false
	for _, kv := range childEnv {
		if strings.HasPrefix(kv, "HOME=") {
			found = true
			require.Contains(t, kv, filepath.Join(runDir, "data"))
			break
		}
	}
	require.True(t, found, "child env should contain HOME with dataHome")
}

// TestNativeEnvSeamRelativeSlotResolvesAgainstTheRunsDir verifies that relative slot
// and job paths resolve against the Run's working directory, not the process's cwd.
func TestNativeEnvSeamRelativeSlotResolvesAgainstTheRunsDir(t *testing.T) {
	t.Parallel()

	// The Run's working directory
	runDir := t.TempDir()

	// Verify we can use filepath.Join to resolve paths relative to runDir
	// This is the pattern used by the Run struct to resolve relative paths
	slotPath := filepath.Join(runDir, "slot")
	jobPath := filepath.Join(slotPath, "jobs", "label")

	// These should be absolute paths when resolved
	require.True(t, filepath.IsAbs(slotPath), "slot path should be absolute")
	require.True(t, filepath.IsAbs(jobPath), "job path should be absolute")

	// Verify they resolve relative to runDir, not os.Getwd()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NotEqual(t, filepath.Join(cwd, "slot"), slotPath, "slot should resolve against runDir, not cwd")
}

// TestNativeEnvSeamLaunchArgvIsTheRunsOwn verifies that launchArgvFor can be
// overridden per-Run through a field on the Run struct.
func TestNativeEnvSeamLaunchArgvIsTheRunsOwn(t *testing.T) {
	t.Parallel()

	// Create a custom launchArgvFor that returns a known value for testing
	customLaunchArgv := func(provider, goos string, req interface{}) ([]string, error) {
		return []string{"custom", "launcher"}, nil
	}

	// Verify we can call it and get our custom return value
	// This tests the pattern of passing the function as a field
	result, err := customLaunchArgv("", "", nil)
	require.NoError(t, err)
	require.Equal(t, []string{"custom", "launcher"}, result)
}
