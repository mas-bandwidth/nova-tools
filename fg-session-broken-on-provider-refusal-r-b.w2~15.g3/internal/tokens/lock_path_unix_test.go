//go:build unix

package tokens

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFoldLockRefusesFIFO(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	path := filepath.Join(out, LockName)
	require.NoError(t, syscall.Mkfifo(path, 0600))
	release, err := TakeFoldLock(out, 0)
	if release != nil {
		release()
	}
	require.Error(t, err, "FIFO lock was accepted")
	info, statErr := os.Lstat(path)
	require.Falsef(t, statErr != nil || info.Mode()&os.ModeNamedPipe == 0, "FIFO changed: %v (%v)", info, statErr)
}
