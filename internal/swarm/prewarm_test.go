package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareLispJobCacheRefusesSymlinkOverlay(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	job := filepath.Join(root, "job")
	source := filepath.Join(job, JobRepo)
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(job, ".cache")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := PrepareLispJobCache(root, source); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("PrepareLispJobCache through symlink error = %v, want refusal", err)
	}
}
