//go:build functional

package ci

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func TestExecTestRunnerRealRun(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Run an existing fast test in allowlist package
	outcome, err := ExecTestRunner(ctx, "github.com/mas-bandwidth/nova-tools/internal/ci/allowlist", "TestCheckReportsWithoutWriting")
	if err != nil {
		t.Fatalf("ExecTestRunner failed: %v", err)
	}
	if !outcome.Passed {
		t.Errorf("expected test to pass, output: %s", outcome.Output)
	}
}

func probeRunTimeout() time.Duration {
	return 2 * time.Second
}

func TestExecTestRunnerTimeoutKillsProcessGroup(t *testing.T) {
	t.Parallel()

	pidFile := filepath.Join(os.TempDir(), "nova-ci-flake-probe.pid")
	_ = os.Remove(pidFile)
	t.Cleanup(func() {
		_ = os.Remove(pidFile)
	})

	// Timeout is 2s, but the probe sleeps for 15s.
	ctx, cancel := context.WithTimeout(context.Background(), probeRunTimeout()) // wall-ok: functional test verifying process group kill on probe timeout
	defer cancel()

	outcome, err := ExecTestRunner(ctx, "github.com/mas-bandwidth/nova-tools/internal/ci/testdata/probes/sleep", "TestSleepProbe")
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}
	if outcome.Passed {
		t.Fatalf("expected test to fail due to timeout, got passed")
	}

	// Read the recorded PID of the test binary.
	raw, readErr := os.ReadFile(pidFile)
	if readErr != nil {
		t.Fatalf("failed to read probe PID file: %v", readErr)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if parseErr != nil {
		t.Fatalf("invalid PID in %s: %q", pidFile, string(raw))
	}

	// Ensure cleanup in case of failure.
	t.Cleanup(func() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	})

	// Assert that the child test binary process did not survive the timeout kill.
	// Give the kernel up to 1s to finish reaping the process group.
	survived := true
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if !isProcessAlive(pid) {
			survived = false
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if survived {
		t.Fatalf("test binary child process (pid %d) survived the timeout kill; process group was not killed", pid)
	}
}
