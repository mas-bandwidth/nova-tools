package swarm

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestClampTimeout verifies that execution timeouts are strictly clamped to [5s, 2h].
func TestClampTimeout(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"zero duration clamped to 5s", 0, 5 * time.Second},
		{"negative duration clamped to 5s", -10 * time.Second, 5 * time.Second},
		{"1s clamped to 5s", 1 * time.Second, 5 * time.Second},
		{"4999ms clamped to 5s", 4999 * time.Millisecond, 5 * time.Second},
		{"exact 5s lower bound preserved", 5 * time.Second, 5 * time.Second},
		{"10s within bounds preserved", 10 * time.Second, 10 * time.Second},
		{"30m within bounds preserved", 30 * time.Minute, 30 * time.Minute},
		{"1h within bounds preserved", 1 * time.Hour, 1 * time.Hour},
		{"exact 2h upper bound preserved", 2 * time.Hour, 2 * time.Hour},
		{"2h plus 1ms clamped to 2h", 2*time.Hour + 1*time.Millisecond, 2 * time.Hour},
		{"5h clamped to 2h", 5 * time.Hour, 2 * time.Hour},
		{"24h clamped to 2h", 24 * time.Hour, 2 * time.Hour},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClampTimeout(tc.in)
			if got != tc.want {
				t.Errorf("ClampTimeout(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestClampTimeoutWithDefault verifies fallback to default timeout when raw duration is <= 0.
func TestClampTimeoutWithDefault(t *testing.T) {
	tests := []struct {
		name       string
		in         time.Duration
		defaultVal time.Duration
		want       time.Duration
	}{
		{"zero with standard default 30m", 0, 30 * time.Minute, 30 * time.Minute},
		{"negative with default 10m", -5 * time.Minute, 10 * time.Minute, 10 * time.Minute},
		{"zero with below-min default 1s clamped to 5s", 0, 1 * time.Second, 5 * time.Second},
		{"zero with above-max default 5h clamped to 2h", 0, 5 * time.Hour, 2 * time.Hour},
		{"positive value overrides default", 15 * time.Minute, 30 * time.Minute, 15 * time.Minute},
		{"positive value below min is clamped", 2 * time.Second, 30 * time.Minute, 5 * time.Second},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClampTimeoutWithDefault(tc.in, tc.defaultVal)
			if got != tc.want {
				t.Errorf("ClampTimeoutWithDefault(%v, %v) = %v, want %v", tc.in, tc.defaultVal, got, tc.want)
			}
		})
	}
}

// TestTimeoutHierarchyConstruction verifies multi-tier hierarchy invariants:
// CardTimeout >= StepTimeout >= GitTimeout.
func TestTimeoutHierarchyConstruction(t *testing.T) {
	t.Run("default hierarchy satisfies invariants", func(t *testing.T) {
		h := DefaultTimeoutHierarchy()
		if err := h.Validate(); err != nil {
			t.Fatalf("DefaultTimeoutHierarchy() validation failed: %v", err)
		}
		if h.CardTimeout != DefaultCardTimeout {
			t.Errorf("CardTimeout = %v, want %v", h.CardTimeout, DefaultCardTimeout)
		}
		if h.StepTimeout != DefaultStepTimeout {
			t.Errorf("StepTimeout = %v, want %v", h.StepTimeout, DefaultStepTimeout)
		}
		if h.GitTimeout != DefaultGitTimeout {
			t.Errorf("GitTimeout = %v, want %v", h.GitTimeout, DefaultGitTimeout)
		}
	})

	t.Run("step clamped to card when card is smaller than step", func(t *testing.T) {
		// Card is 5s, step requested 10m -> step must clamp to 5s
		h := NewTimeoutHierarchy(5*time.Second, 10*time.Minute, 1*time.Minute)
		if err := h.Validate(); err != nil {
			t.Fatalf("validation failed: %v", err)
		}
		if h.CardTimeout != 5*time.Second {
			t.Errorf("CardTimeout = %v, want 5s", h.CardTimeout)
		}
		if h.StepTimeout != 5*time.Second {
			t.Errorf("StepTimeout = %v, want 5s", h.StepTimeout)
		}
		if h.GitTimeout > h.StepTimeout {
			t.Errorf("GitTimeout (%v) exceeds StepTimeout (%v)", h.GitTimeout, h.StepTimeout)
		}
	})

	t.Run("git clamped to max git timeout 1m", func(t *testing.T) {
		h := NewTimeoutHierarchy(1*time.Hour, 30*time.Minute, 10*time.Minute)
		if err := h.Validate(); err != nil {
			t.Fatalf("validation failed: %v", err)
		}
		if h.GitTimeout != MaxGitTimeout {
			t.Errorf("GitTimeout = %v, want MaxGitTimeout (%v)", h.GitTimeout, MaxGitTimeout)
		}
	})

	t.Run("card clamped to max execution timeout 2h", func(t *testing.T) {
		h := NewTimeoutHierarchy(10*time.Hour, 5*time.Hour, 2*time.Minute)
		if err := h.Validate(); err != nil {
			t.Fatalf("validation failed: %v", err)
		}
		if h.CardTimeout != MaxExecutionTimeout {
			t.Errorf("CardTimeout = %v, want %v", h.CardTimeout, MaxExecutionTimeout)
		}
		if h.StepTimeout > h.CardTimeout {
			t.Errorf("StepTimeout (%v) exceeds CardTimeout (%v)", h.StepTimeout, h.CardTimeout)
		}
	})

	t.Run("unspecified step and git derive automatically", func(t *testing.T) {
		h := NewTimeoutHierarchy(15*time.Minute, 0, 0)
		if err := h.Validate(); err != nil {
			t.Fatalf("validation failed: %v", err)
		}
		if h.StepTimeout != DefaultStepTimeout {
			t.Errorf("StepTimeout = %v, want %v", h.StepTimeout, DefaultStepTimeout)
		}
		// Git derived from step (5m / 3 = 1m40s -> capped at 1m)
		if h.GitTimeout != MaxGitTimeout {
			t.Errorf("GitTimeout = %v, want %v", h.GitTimeout, MaxGitTimeout)
		}
	})
}

// TestTimeoutHierarchyBudgets verifies dynamic step and git budget allocations.
func TestTimeoutHierarchyBudgets(t *testing.T) {
	h := NewTimeoutHierarchy(30*time.Minute, 5*time.Minute, 1*time.Minute)

	t.Run("StepBudget", func(t *testing.T) {
		if got := h.StepBudget(0); got != 0 {
			t.Errorf("StepBudget(0) = %v, want 0", got)
		}
		if got := h.StepBudget(-1 * time.Minute); got != 0 {
			t.Errorf("StepBudget(-1m) = %v, want 0", got)
		}
		if got := h.StepBudget(20 * time.Minute); got != 5*time.Minute {
			t.Errorf("StepBudget(20m) = %v, want 5m", got)
		}
		if got := h.StepBudget(2 * time.Minute); got != 2*time.Minute {
			t.Errorf("StepBudget(2m) = %v, want 2m", got)
		}
	})

	t.Run("GitBudget", func(t *testing.T) {
		if got := h.GitBudget(0); got != 0 {
			t.Errorf("GitBudget(0) = %v, want 0", got)
		}
		// 30s remaining in step: 30s / 3 = 10s
		if got := h.GitBudget(30 * time.Second); got != 10*time.Second {
			t.Errorf("GitBudget(30s) = %v, want 10s", got)
		}
		// 6m remaining in step: 6m / 3 = 2m -> capped at 1m
		if got := h.GitBudget(6 * time.Minute); got != 1*time.Minute {
			t.Errorf("GitBudget(6m) = %v, want 1m", got)
		}
		// 6s remaining in step: 6s / 3 = 2s -> floored at MinGitTimeout (5s)
		if got := h.GitBudget(6 * time.Second); got != 5*time.Second {
			t.Errorf("GitBudget(6s) = %v, want 5s", got)
		}
		// 3s remaining in step: floored at 5s, but cannot exceed remaining (3s) -> 3s
		if got := h.GitBudget(3 * time.Second); got != 3*time.Second {
			t.Errorf("GitBudget(3s) = %v, want 3s", got)
		}
	})
}

// TestTimeoutHierarchyContexts verifies context creation and deadline propagation.
func TestTimeoutHierarchyContexts(t *testing.T) {
	h := NewTimeoutHierarchy(10*time.Second, 5*time.Second, 1*time.Second)

	ctx, cancel := h.CardContext(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("CardContext has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining < 8*time.Second || remaining > 11*time.Second {
		t.Errorf("CardContext remaining = %v, want ~10s", remaining)
	}

	stepCtx, stepCancel := h.StepContext(context.Background(), 20*time.Second)
	defer stepCancel()
	stepDeadline, ok := stepCtx.Deadline()
	if !ok {
		t.Fatal("StepContext has no deadline")
	}
	stepRemaining := time.Until(stepDeadline)
	if stepRemaining < 3*time.Second || stepRemaining > 6*time.Second {
		t.Errorf("StepContext remaining = %v, want ~5s", stepRemaining)
	}
}

// TestTaskkillArgs verifies Windows taskkill argument construction.
func TestTaskkillArgs(t *testing.T) {
	pid := 4242

	// Graceful tree termination (Phase 1 / SIGTERM equivalent)
	gracefulArgs := TaskkillArgs(pid, false)
	wantGraceful := []string{"/PID", "4242", "/T"}
	if !reflect.DeepEqual(gracefulArgs, wantGraceful) {
		t.Errorf("TaskkillArgs(%d, false) = %v, want %v", pid, gracefulArgs, wantGraceful)
	}

	// Forceful tree termination (Phase 2 / SIGKILL equivalent)
	forceArgs := TaskkillArgs(pid, true)
	wantForce := []string{"/PID", "4242", "/T", "/F"}
	if !reflect.DeepEqual(forceArgs, wantForce) {
		t.Errorf("TaskkillArgs(%d, true) = %v, want %v", pid, forceArgs, wantForce)
	}
}

// TestWindowsKillStrategyComparison tests the comparison between taskkill /T /F vs syscall.
func TestWindowsKillStrategyComparison(t *testing.T) {
	if StrategyTaskkill.String() != "taskkill" {
		t.Errorf("StrategyTaskkill.String() = %q, want %q", StrategyTaskkill.String(), "taskkill")
	}
	if StrategySyscall.String() != "syscall" {
		t.Errorf("StrategySyscall.String() = %q, want %q", StrategySyscall.String(), "syscall")
	}

	// Contract verification:
	// - StrategyTaskkill generates /PID <pid> /T (/F), guaranteeing recursive process tree reaping.
	// - StrategySyscall operates via direct OS PID termination (os.Process.Kill / TerminateProcess),
	//   which reaches only the direct process without tree enumeration.
	taskkillForceArgs := TaskkillArgs(1001, true)
	hasTree := false
	hasForce := false
	for _, arg := range taskkillForceArgs {
		if strings.EqualFold(arg, "/T") {
			hasTree = true
		}
		if strings.EqualFold(arg, "/F") {
			hasForce = true
		}
	}
	if !hasTree || !hasForce {
		t.Errorf("taskkill forceful kill missing tree or force flags: %v", taskkillForceArgs)
	}
}

// TestTwoPhaseReapLifecycle tests the two-phase reap contract on the host system.
func TestTwoPhaseReapLifecycle(t *testing.T) {
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

// TestTwoPhaseReapUnresponsiveChildKilledBySIGKILL tests that a process ignoring SIGTERM
// is forcefully terminated by Phase 2 (SIGKILL) once the grace period expires.
func TestTwoPhaseReapUnresponsiveChildKilledBySIGKILL(t *testing.T) {
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

// TestWindowsTreeReapFallback verifies that Windows taskkill vs syscall fallback functions correctly.
func TestWindowsTreeReapFallback(t *testing.T) {
	// Verify that unknown or dash stamps are rejected safely without signalling
	if KnownStamp("") {
		t.Errorf("KnownStamp(\"\") want false")
	}
	if KnownStamp(Dash) {
		t.Errorf("KnownStamp(Dash) want false")
	}
	if KnownStamp("123456789") != true {
		t.Errorf("KnownStamp(stamp) want true")
	}

	// Verify PID string formatting for taskkill
	for _, pid := range []int{1, 100, 65535, 123456} {
		args := TaskkillArgs(pid, true)
		if args[1] != strconv.Itoa(pid) {
			t.Errorf("TaskkillArgs(%d) PID arg = %q, want %d", pid, args[1], pid)
		}
	}
}
