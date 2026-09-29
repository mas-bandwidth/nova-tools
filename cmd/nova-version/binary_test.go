package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

var (
	builtNovaVersionOnce sync.Once
	builtNovaVersionPath string
	builtNovaVersionErr  error
)

func buildNovaVersion(t *testing.T) string {
	t.Helper()
	builtNovaVersionOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nova-version-bin-")
		if err != nil {
			builtNovaVersionErr = err
			return
		}
		build := exec.Command("go", "build", "-o", dir+string(os.PathSeparator), "./cmd/nova-version")
		build.Env = goenv.Clean(os.Environ())
		build.Dir = repoRoot(t)
		out, err := build.CombinedOutput()
		if err != nil {
			builtNovaVersionErr = fmt.Errorf("building nova-version: %w\n%s", err, out)
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			builtNovaVersionErr = err
			return
		}
		if len(entries) != 1 {
			builtNovaVersionErr = fmt.Errorf("expected 1 binary in %s, got %d", dir, len(entries))
			return
		}
		builtNovaVersionPath = filepath.Join(dir, entries[0].Name())
	})
	if builtNovaVersionErr != nil {
		t.Fatal(builtNovaVersionErr)
	}
	return builtNovaVersionPath
}
