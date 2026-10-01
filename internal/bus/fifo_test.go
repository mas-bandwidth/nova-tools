//go:build !windows

package bus

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A FIFO planted at a lane's INDEX must never park the lane reader: the read refuses it by
// name, never opening the pipe (issue #233).
func TestReadLaneIndexDoesNotBlockOnAPlantedFIFO(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "bus")
	index := filepath.Join(root, "from-x", IndexName)
	require.NoError(t, os.MkdirAll(filepath.Dir(index), 0o755))
	if err := syscall.Mkfifo(index, 0o644); err != nil {
		t.Skipf("this platform will not make a FIFO: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ReadLaneIndex(root, "from-x")
		done <- err
	}()
	select {
	case err := <-done:
		require.Error(t, err, "a FIFO read as a lane INDEX")
		require.Contains(t, err.Error(), "fifo", "the read refusal does not name the kind fifo: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("STILL BLOCKED after 30s reading a FIFO at INDEX: the lane reader is wedged")
	}
}
