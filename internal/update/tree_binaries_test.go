//go:build functional

package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
	"github.com/stretchr/testify/require"
)

var (
	treeOnce     sync.Once
	treeDir      string
	treeBuildErr error
)

func exeName(n string) string {
	if runtime.GOOS == "windows" {
		return n + ".exe"
	}
	return n
}

// buildTreeBinaries builds this tree's nova-update once per package run, so a test that
// needs a real reporter process runs the tree's and never a version somebody installed.
func buildTreeBinaries(t *testing.T) string {
	t.Helper()
	treeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nova-update-tree-")
		if err != nil {
			treeBuildErr = err
			return
		}
		treeDir = dir
		pkg := "./cmd/nova-update"
		build := exec.Command("go", "build", "-buildvcs=false", "-o", filepath.Join(dir, exeName(filepath.Base(pkg))), pkg)
		build.Dir = filepath.Join("..", "..")
		build.Env = goenv.Clean(os.Environ())
		if out, err := build.CombinedOutput(); err != nil {
			treeBuildErr = fmt.Errorf("go build %s: %v\n%s", pkg, err, out)
		}
	})
	require.NoError(t, treeBuildErr)
	return treeDir
}

func init() {
	testCleanup = func() {
		if treeDir != "" {
			os.RemoveAll(treeDir)
		}
	}
}
