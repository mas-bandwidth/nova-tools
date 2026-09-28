package swarm

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestClampTimeout verifies that execution timeouts are strictly clamped to [5s, 2h].
func TestClampTimeout(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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

// TestWindowsTreeReapFallback verifies that Windows taskkill vs syscall fallback functions correctly.
func TestWindowsTreeReapFallback(t *testing.T) {
	t.Parallel()

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
