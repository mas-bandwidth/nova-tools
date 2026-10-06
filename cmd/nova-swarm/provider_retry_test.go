package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A LAUNCH THAT DIES FAST ON A PROVIDER 5XX IS RETRIED (issue #900). These tests hold the
// inherited grace path: the retry keeps the task, the usage rows carry attempt=1,2,3, and a
// slow failure is not retried. The tail text is not evidence the provider never accepted
// the request.

func TestPersistUnknownFallsBackWhenTheMarkerCannotBeWritten(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(job, "provider-acceptance"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(job, "harness-output.log"), 0o755))
	require.NoError(t, persistUnknown(job))
	raw, err := os.ReadFile(filepath.Join(job, "harness.log"))
	require.NoError(t, err, "the fallback log does not hold the unknown: %q, %v", raw, err)
	require.Contains(t, string(raw), "why=unknown-acceptance", "the fallback log does not hold the unknown: %q, %v", raw, err)
}

func TestPersistUnknownFailsWhenNothingCanBeWritten(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	require.NoError(t, os.Chmod(job, 0o555))
	t.Cleanup(func() { _ = os.Chmod(job, 0o755) })
	require.Error(t, persistUnknown(job), "an unwritable job recorded the unknown")
}
