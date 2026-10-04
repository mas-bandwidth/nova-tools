package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAJobConfigWriteFailureNamesTheFile: a writeJobConfig refusal that fails to write the
// leaf file names opencode.json and says written, so the operator looks at the file and not
// at a directory that was made. A data home whose .config/opencode exists as a directory
// holding opencode.json as a directory lets MkdirAll succeed and WriteFile fail with the
// "is a directory" error; the returned reason must carry the leaf, the word written, and the
// underlying error, in the same shape copyAuth's two writes take (ONBOARDING point 2: a
// refusal says what the input WANTS; copyAuth below returns "the auth copy <file> could not
// be written").
func TestAJobConfigWriteFailureNamesTheFile(t *testing.T) {
	t.Parallel()

	dataHome := t.TempDir()
	ocDir := filepath.Join(dataHome, ".config", "opencode")
	require.NoError(t, os.MkdirAll(filepath.Join(ocDir, "opencode.json"), 0o755), "the leaf is a directory, so the write fails and the mkdir does not")

	_, reason, proxy := writeJobConfig(nativeRunConfig{}, "fake", dataHome, t.TempDir(), nil, nil)
	assert.Nil(t, proxy)
	require.NotEmpty(t, reason, "writing opencode.json over a directory is refused")
	assert.Contains(t, reason, "opencode.json", "the refusal names the leaf file that could not be written: %s", reason)
	assert.Contains(t, reason, "written", "the refusal says the file could not be written: %s", reason)
	assert.NotContains(t, reason, "could not be made", "the refusal does not blame the directory that was made: %s", reason)
}
