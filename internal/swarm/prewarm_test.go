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

func TestSpecNamesExactTipPrewarmAndItsFleetMeasurement(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-SWARM.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.Join(strings.Fields(string(raw)), " ")
	for _, want := range []string{
		"## Exact-tip bench prewarm (#2498 S3)",
		"modules, ordinary builds, compiled Go test binaries and ASDF FASLs",
		"hidden until all four phases succeed",
		"`make test` in a fresh job on each adopted bench finishes in under 60 seconds",
		"A PREWARM receipt proves preparation, not fleet adoption",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("SPEC-SWARM.md does not contain %q", want)
		}
	}
}
