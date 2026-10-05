//go:build unix

package friend

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlockHeldSeesAnotherDescriptorsLockAndNothingElse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	assert.False(t, FlockHeld(filepath.Join(dir, "missing.lock")), "no file: nobody holds it")
	path := filepath.Join(dir, "t1.lock")
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close() // ignored: a test's lock file
	assert.False(t, FlockHeld(path), "a file nobody locked")
	require.NoError(t, syscall.Flock(int(f.Fd()), syscall.LOCK_EX))
	assert.True(t, FlockHeld(path), "locked on another descriptor, as the Codex app holds a thread")
	require.NoError(t, syscall.Flock(int(f.Fd()), syscall.LOCK_UN))
	assert.False(t, FlockHeld(path), "released")
}
