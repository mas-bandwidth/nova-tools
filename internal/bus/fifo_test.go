//go:build !windows

package bus

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

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
	// Called directly: a read that opened the pipe would block here, and the test binary's
	// -timeout names the goroutine parked in it. The refusal by kind is the assertion.
	_, err := ReadLaneIndex(root, "from-x")
	require.Error(t, err, "a FIFO read as a lane INDEX")
	require.Contains(t, err.Error(), "fifo", "the read refusal does not name the kind fifo: %v", err)
}
