//go:build unix

package swarm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestASharedRuntimeDirIsGrantedOnlyAsARealDirectory: an absent directory is made 0777 and
// named; a real one is named; a link at the name, or at its shm, is never named, so the wall
// is never handed a link's target.
func TestASharedRuntimeDirIsGrantedOnlyAsARealDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	made := filepath.Join(root, "made")
	real := filepath.Join(root, "real")
	require.NoError(t, os.Mkdir(real, 0o777))
	target := filepath.Join(root, "target")
	require.NoError(t, os.Mkdir(target, 0o700))
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(target, link))
	shmLink := filepath.Join(root, "shmlink")
	require.NoError(t, os.Mkdir(shmLink, 0o777))
	require.NoError(t, os.Symlink(target, filepath.Join(shmLink, "shm")))

	got := ensureSharedDirs([]string{made, real, link, shmLink})
	assert.Equal(t, []string{made, real}, got)
	fi, err := os.Stat(made)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o777), fi.Mode().Perm(), "made with dotnet's own mode")
}
