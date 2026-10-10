//go:build functional

package ci

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/stretchr/testify/require"
)

// linterCache holds built linter binaries, cached per test run.
var linterCache struct {
	sync.Once
	mu sync.Mutex
	tools map[string]string
}

// buildLinterTool builds a linter tool at pkg path and caches the binary.
// It builds once per test run (not once per test), reusing the binary
// across staticcheck, errcheck, and other linter tests.
func buildLinterTool(t *testing.T, ctx context.Context, root, pkg string) string {
	linterCache.Do(func() {
		linterCache.tools = make(map[string]string)
	})

	linterCache.mu.Lock()
	defer linterCache.mu.Unlock()

	if bin, ok := linterCache.tools[pkg]; ok {
		return bin
	}

	bin := filepath.Join(t.TempDir(), filepath.Base(pkg))
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, pkg)
	cmd.Dir = root
	cmd.Env = goenv.Clean(os.Environ())
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "go build %s: %s", pkg, out)

	linterCache.tools[pkg] = bin
	return bin
}
