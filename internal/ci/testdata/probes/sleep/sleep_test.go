package sleep

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestSleepProbe(t *testing.T) {
	pidFile := filepath.Join(os.TempDir(), "nova-ci-flake-probe.pid")
	_ = os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644)
	time.Sleep(15 * time.Second)
}
