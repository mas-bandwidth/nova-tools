//go:build functional

package swarm

import (
	"bufio"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). The rest of the file's tests stay in the
// unit tier.

// TestTwoPhaseReapLifecycle tests the two-phase reap contract on the host system.
func TestTwoPhaseReapLifecycle(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("Unix process group tests run under POSIX; Windows compatibility verified via TaskkillArgs & cross-compile")
	}

	// Spawn a subprocess that traps SIGTERM and ignores it for 1s, then exits cleanly.
	// This proves Phase 1 (SIGTERM) allows graceful flush within the 3s TerminateGrace window.
	cmd := exec.Command("sleep", "60")
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start test process: %v", err)
	}
	exited := waitSignal(cmd)
	pid := cmd.Process.Pid
	started := StartStamp(pid)

	if !Alive(pid, started) {
		t.Fatalf("process %d was not alive after start", pid)
	}

	survived := TwoPhaseReap(pid, started)

	if survived {
		t.Fatalf("process %d survived TwoPhaseReap", pid)
	}
	// The event, not the clock: a cooperative child dies of Phase 1's SIGTERM,
	// so Phase 2's SIGKILL is never needed.
	if sig := <-exited; sig != syscall.SIGTERM {
		t.Errorf("cooperative child ended by %v, want SIGTERM (Phase 1)", sig)
	}
	if Alive(pid, started) {
		t.Fatalf("process %d is still reported alive after reap", pid)
	}
}

// TestTwoPhaseReapUnresponsiveChildKilledBySIGKILL tests that a process ignoring SIGTERM
// is forcefully terminated by Phase 2 (SIGKILL) once the grace period expires.
func TestTwoPhaseReapUnresponsiveChildKilledBySIGKILL(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("Unix process group tests run under POSIX")
	}

	// The child prints "ready" only after SIG_IGN is installed. A fixed sleep
	// raced interpreter startup on a loaded bench: SIGTERM arrived before the
	// handler, the child died in Phase 1, and the reap finished under grace.
	cmd := exec.Command("python3", "-c", "import signal, time; signal.signal(signal.SIGTERM, signal.SIG_IGN); print('ready', flush=True); time.sleep(30)")
	ownGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start uncooperative process: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	ready := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err == nil && strings.TrimSpace(line) != "ready" {
			err = fmt.Errorf("unexpected readiness line %q", line)
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("uncooperative process never became ready: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("uncooperative process not ready after 10s")
	}
	exited := waitSignal(cmd)
	pid := cmd.Process.Pid
	started := StartStamp(pid)

	survived := TwoPhaseReapWithGrace(pid, started, 150*time.Millisecond)

	if survived {
		t.Fatalf("uncooperative process %d survived two-phase reap", pid)
	}
	// The event, not the clock: the child ignored SIGTERM, so only Phase 2's
	// SIGKILL can have ended it.
	if sig := <-exited; sig != syscall.SIGKILL {
		t.Errorf("uncooperative child ended by %v, want SIGKILL (Phase 2)", sig)
	}
	if Alive(pid, started) {
		t.Fatalf("process %d still alive after two-phase SIGKILL reap", pid)
	}
}

// waitSignal reaps cmd in the background and reports the signal that ended
// it (0 if it exited without one), so a reap test asserts which phase killed
// the child instead of timing the reap.
func waitSignal(cmd *exec.Cmd) <-chan syscall.Signal {
	out := make(chan syscall.Signal, 1)
	go func() {
		_ = cmd.Wait()
		var sig syscall.Signal
		if ps := cmd.ProcessState; ps != nil {
			if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				sig = ws.Signal()
			}
		}
		out <- sig
	}()
	return out
}
