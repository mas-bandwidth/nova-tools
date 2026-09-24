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

// Standing probe for issue #233, INDEX half: a symlink and a FIFO planted at a lane's
// INDEX, in that order, each refused by kind and never followed or blocked on. The
// per-site tests in symlink_discipline_test.go and fifo_test.go cover each plant; this
// is the sequence both probes walk together.

func TestFriendSequencePlantedIndexIsRefused(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "bus")
	body := "deadbeef\tfrom-x/2026-note.md\t2026-09-13T00:00:00Z\t-\t-\n"
	v := victimHolding(t, dir, body)
	link := filepath.Join(root, "from-x", IndexName)
	plant(t, v, link)
	_, err := ReadLaneIndex(root, "from-x")
	if err == nil {
		t.Fatal("ReadLaneIndex read through a planted symlink at INDEX")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("the symlink refusal does not name the kind: %v", err)
	}
	unchangedHolding(t, v, body)

	fifoRoot := filepath.Join(dir, "bus-fifo")
	fifo := filepath.Join(fifoRoot, "from-x", IndexName)
	if err := os.MkdirAll(filepath.Dir(fifo), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("this platform will not make a FIFO: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ReadLaneIndex(fifoRoot, "from-x")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a FIFO read as a lane INDEX")
		}
		if !strings.Contains(err.Error(), "fifo") {
			t.Fatalf("the fifo refusal does not name the kind: %v", err)
		}
	case <-time.After(plantedIndexWait()):
		t.Fatal("STILL BLOCKED after waiting on a FIFO at INDEX: the lane reader is wedged")
	}
}

func plantedIndexWait() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}
