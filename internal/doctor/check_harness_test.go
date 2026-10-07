package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctorHarnessCheckNamesAFriendWhoseHarnessIsMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Create a temporary friend directory structure
	tempDir := t.TempDir()
	// Use relative path for friends directory under root
	friendDir := "friends"
	fullFriendDir := filepath.Join(tempDir, friendDir)
	require.NoError(t, os.MkdirAll(fullFriendDir, 0o755))

	// Create a friend with harness info but missing binary on PATH
	fullFriend1Dir := filepath.Join(fullFriendDir, "friend1")
	require.NoError(t, os.MkdirAll(fullFriend1Dir, 0o755))

	// Write harness file
	require.NoError(t, os.WriteFile(filepath.Join(fullFriend1Dir, "harness"), []byte("claude\n"), 0o644))

	// Write beat file (claude one-shot mode)
	beatContent := "FRIEND-BEAT OK friend1 harness=claude mode=one-shot row_config_dir=/home/user/claude-config"
	require.NoError(t, os.WriteFile(filepath.Join(fullFriend1Dir, "beat"), []byte(beatContent), 0o644))

	// Create a fake env
	fakeEnv := fakeEnv{
		env: map[string]string{
			"NOVA_FRIEND_HOME": friendDir,
			"HOME":             "/home/user",
		},
		root: tempDir,
		exec: func(name string, args ...string) (string, error) {
			return "", nil // No execution needed for this test
		},
	}

	result := checkHarness(ctx, fakeEnv)

	// The harness check should fail because claude binary is not on PATH
	assert.Equal(t, Fail, result.Status, "check should fail when harness binary is missing")
	assert.Contains(t, result.Evidence, "claude", "evidence should mention the missing harness")
}

func TestDoctorHarnessCheckWithValidFriend(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tempDir := t.TempDir()

	// Create friend directory with relative path
	friendDir := "friends"
	fullFriendDir := filepath.Join(tempDir, friendDir)
	require.NoError(t, os.MkdirAll(fullFriendDir, 0o755))

	// Create a friend with all requirements met
	fullFriend1Dir := filepath.Join(fullFriendDir, "friend1")
	require.NoError(t, os.MkdirAll(fullFriend1Dir, 0o755))

	// Write harness file
	require.NoError(t, os.WriteFile(filepath.Join(fullFriend1Dir, "harness"), []byte("opencode\n"), 0o644))

	// Write beat file (opencode doesn't need config dir)
	beatContent := "FRIEND-BEAT OK friend1 harness=opencode mode=batch"
	require.NoError(t, os.WriteFile(filepath.Join(fullFriend1Dir, "beat"), []byte(beatContent), 0o644))

	// Add opencode binary to PATH
	binDir := "bin"
	fullBinDir := filepath.Join(tempDir, binDir)
	require.NoError(t, os.MkdirAll(fullBinDir, 0o755))
	// Create a mock opencode binary
	require.NoError(t, os.WriteFile(filepath.Join(fullBinDir, "opencode"), []byte("#!/bin/sh\n"), 0o755))

	fakeEnv := fakeEnv{
		env: map[string]string{
			"NOVA_FRIEND_HOME": friendDir,
			"PATH":             binDir,
			"HOME":             "/home/user",
		},
		root: tempDir,
		exec: func(name string, args ...string) (string, error) {
			return "", nil
		},
	}

	result := checkHarness(ctx, fakeEnv)

	assert.Equal(t, OK, result.Status, "check should pass when all requirements are met")
}

func TestDoctorHarnessCheckConfigDirMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tempDir := t.TempDir()

	// Create friend directory with relative path
	friendDir := "friends"
	fullFriendDir := filepath.Join(tempDir, friendDir)
	require.NoError(t, os.MkdirAll(fullFriendDir, 0o755))

	// Create a claude friend with config dir pointing to non-existent directory
	fullFriend1Dir := filepath.Join(fullFriendDir, "friend1")
	require.NoError(t, os.MkdirAll(fullFriend1Dir, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(fullFriend1Dir, "harness"), []byte("claude\n"), 0o644))

	// Write beat with config_dir pointing to non-existent directory
	beatContent := "FRIEND-BEAT OK friend1 harness=claude mode=one-shot row_config_dir=/home/user/nonexistent-claude"
	require.NoError(t, os.WriteFile(filepath.Join(fullFriend1Dir, "beat"), []byte(beatContent), 0o644))

	// Add claude binary to PATH
	binDir := "bin"
	fullBinDir := filepath.Join(tempDir, binDir)
	require.NoError(t, os.MkdirAll(fullBinDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fullBinDir, "claude"), []byte("#!/bin/sh\n"), 0o755))

	fakeEnv := fakeEnv{
		env: map[string]string{
			"NOVA_FRIEND_HOME": friendDir,
			"PATH":             binDir,
			"HOME":             "/home/user",
		},
		root: tempDir,
		exec: func(name string, args ...string) (string, error) {
			return "", nil
		},
	}

	result := checkHarness(ctx, fakeEnv)

	assert.Equal(t, Fail, result.Status, "check should fail when claude config dir is missing")
	assert.Contains(t, result.Evidence, "config directory")
}
