package sleep

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestSleepProbe(t *testing.T) {
	dir := os.Getenv("NOVA_CI_FLAKE_PROBE_DIR")
	if dir == "" {
		t.Fatal("missing NOVA_CI_FLAKE_PROBE_DIR environment variable")
	}
	pidFile := filepath.Join(dir, "probe.pid")
	readyFile := filepath.Join(dir, "probe.ready")

	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("failed to write pid file: %v", err)
	}
	if err := os.WriteFile(readyFile, []byte("ready\n"), 0o600); err != nil {
		t.Fatalf("failed to write ready file: %v", err)
	}
	time.Sleep(30 * time.Second)
}
