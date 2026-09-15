//go:build !windows

package bus

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A FIFO planted at a lane's INDEX must never park the lane reader: the read refuses it by
// name, never opening the pipe (issue #233).
func TestReadLaneIndexDoesNotBlockOnAPlantedFIFO(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "bus")
	index := filepath.Join(root, "from-x", IndexName)
	if err := os.MkdirAll(filepath.Dir(index), 0o755); err != nil {
		t.Fatal(err)
	}
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
		if err == nil {
			t.Fatal("a FIFO read as a lane INDEX")
		}
		if !strings.Contains(err.Error(), "fifo") {
			t.Fatalf("the read refusal does not name the kind fifo: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("STILL BLOCKED after 5s reading a FIFO at INDEX: the lane reader is wedged")
	}
}
