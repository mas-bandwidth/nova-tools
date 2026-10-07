package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestACopyAuthFailureLeavesNoAuthCopy: a copyAuth refusal raised after the first write
// leaves no auth copy on disk. A data home whose opencode entry is a regular file makes
// the opencode directory fail to appear with dataHome/auth.json already holding the
// plaintext key, and the run path returns exit 2 at once, before the defer that removes
// the copy, so copyAuth itself unlinks what it wrote before returning the reason
// (docs/SPEC-SECRETS.md, the dogfooding ten: a plaintext key never outlives its card).
func TestACopyAuthFailureLeavesNoAuthCopy(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	src := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(src, []byte(`{"fake":"the-fake-secret"}`), 0o600))
	dataHome := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dataHome, "opencode"), []byte("a regular file, not a directory\n"), 0o644))
	reason := copyAuth(src, "fake", dataHome)
	require.NotEmpty(t, reason, "copyAuth refuses when the opencode entry is a regular file")
	_, err := os.Lstat(filepath.Join(dataHome, "auth.json"))
	assert.True(t, os.IsNotExist(err), "the refused copy left %s on disk with the plaintext key", filepath.Join(dataHome, "auth.json"))
}
