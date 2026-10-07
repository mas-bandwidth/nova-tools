package swarm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrepareLispJobCacheRefusesSymlinkOverlay(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	job := filepath.Join(root, "job")
	source := filepath.Join(job, JobRepo)
	require.NoError(t, os.MkdirAll(source, 0o755))
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(job, ".cache")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	err := PrepareLispJobCache(root, source)
	require.Error(t, err, "PrepareLispJobCache through symlink error = %v, want refusal", err)
	require.Contains(t, err.Error(), "not a directory", "PrepareLispJobCache through symlink error = %v, want refusal", err)
}
