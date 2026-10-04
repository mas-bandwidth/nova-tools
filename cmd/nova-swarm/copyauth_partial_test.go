package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestACopyAuthFailureLeavesNoAuthCopy: a refusal raised after the first copy was
// written removes it before returning, so no plaintext key outlives the refusal. Here
// dataHome/opencode is a regular file, so dataHome/auth.json is written and the second
// copy cannot be; the refusal must not leave the first one behind. docs/SPEC-SECRETS.md,
// the bench standard that fails loudly on a plaintext key file: a refusal is not a mode
// that leaves one on disk.
func TestACopyAuthFailureLeavesNoAuthCopy(t *testing.T) {
	t.Parallel()

	src := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(src, []byte(`{"fake":"the-fake-secret"}`), 0o600))
	dataHome := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dataHome, "opencode"), []byte("not a directory"), 0o600))

	reason := copyAuth(src, "fake", dataHome)
	require.NotEmpty(t, reason, "copyAuth returned no refusal though dataHome/opencode is not a directory")
	_, err := os.Lstat(filepath.Join(dataHome, "auth.json"))
	assert.True(t, os.IsNotExist(err), "the refusal left the plaintext auth copy %s on disk", filepath.Join(dataHome, "auth.json"))
}
