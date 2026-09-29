package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

func buildNovaVersion(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	build := exec.Command("go", "build", "-o", dir+string(os.PathSeparator), "./cmd/nova-version")
	build.Env = goenv.Clean(os.Environ())
	build.Dir = repoRoot(t)
	out, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("building nova-version: %v\n%s", err, out)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 binary in %s, got %d", dir, len(entries))
	}
	return filepath.Join(dir, entries[0].Name())
}
