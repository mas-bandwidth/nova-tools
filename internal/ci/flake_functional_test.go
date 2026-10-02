//go:build functional && !windows

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

func testWait() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

func TestExecTestRunnerRealRun(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), testWait())
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

func TestExecTestRunnerTimeoutKillsProcessGroup(t *testing.T) {
	t.Parallel()

	probeDir := t.TempDir()
	pidFile := filepath.Join(probeDir, "probe.pid")
	readyFile := filepath.Join(probeDir, "probe.ready")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type runnerResult struct {
		outcome RunOutcome
		err     error
	}
	ch := make(chan runnerResult, 1)
	go func() {
		outcome, err := ExecTestRunnerEnv(ctx, "github.com/mas-bandwidth/nova-tools/internal/ci/testdata/probes/sleep", "TestSleepProbe", []string{"NOVA_CI_FLAKE_PROBE_DIR=" + probeDir})
		ch <- runnerResult{outcome: outcome, err: err}
	}()

	// Wait for readiness handshake from the probe.
	waitLimit := testWait()
	deadline := time.Now().Add(waitLimit)
	var pid int
	for time.Now().Before(deadline) {
		if _, err := os.Stat(readyFile); err == nil {
			raw, readErr := os.ReadFile(pidFile)
			if readErr == nil {
				if p, parseErr := strconv.Atoi(strings.TrimSpace(string(raw))); parseErr == nil && p > 0 {
					pid = p
					break
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	if pid <= 0 {
		cancel()
		t.Fatalf("probe test did not signal readiness within %v", waitLimit)
	}

	// Safe owned-process cleanup: only kill the verified probe pid if it survives.
	t.Cleanup(func() {
		if isProcessAlive(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})

	// Cancel context to trigger process group kill.
	cancel()

	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("unexpected runner error: %v", res.err)
		}
		if res.outcome.Passed {
			t.Fatalf("expected test to fail due to timeout/cancellation, got passed")
		}
	case <-time.After(testWait()):
		t.Fatal("ExecTestRunner did not return after context cancellation")
	}

	// Assert that the child process did not survive the cancellation kill.
	reapDeadline := time.Now().Add(testWait())
	survived := true
	for time.Now().Before(reapDeadline) {
		if !isProcessAlive(pid) {
			survived = false
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if survived {
		t.Fatalf("test binary child process (pid %d) survived the cancellation kill; process group was not killed", pid)
	}
}

func TestExecTestRunnerNormalExitKillsProcessGroup(t *testing.T) {
	t.Parallel()

	probeDir := t.TempDir()
	readyFile := filepath.Join(probeDir, "descendant.ready")

	ctx, cancel := context.WithTimeout(context.Background(), testWait())
	defer cancel()

	outcome, err := ExecTestRunnerEnv(ctx, "github.com/mas-bandwidth/nova-tools/internal/ci/testdata/probes/descendant", "TestDescendantProbe", []string{"NOVA_CI_FLAKE_PROBE_DIR=" + probeDir})
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}
	if !outcome.Passed {
		t.Fatalf("expected test to pass, output: %s", outcome.Output)
	}

	raw, err := os.ReadFile(readyFile)
	if err != nil {
		t.Fatalf("failed to read descendant ready file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		t.Fatalf("invalid descendant pid %q: %v", string(raw), err)
	}

	t.Cleanup(func() {
		if isProcessAlive(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})

	// Assert that the descendant process did not survive the normal exit cleanup.
	reapDeadline := time.Now().Add(testWait())
	survived := true
	for time.Now().Before(reapDeadline) {
		if !isProcessAlive(pid) {
			survived = false
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if survived {
		t.Fatalf("descendant process (pid %d) survived normal parent exit; process group was not cleaned up", pid)
	}
}
