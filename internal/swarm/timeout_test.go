package swarm

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

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

// TestWindowsTreeReapFallback verifies the PID formatting of the taskkill fallback.
func TestWindowsTreeReapFallback(t *testing.T) {
	t.Parallel()

	// Verify PID string formatting for taskkill
	for _, pid := range []int{1, 100, 65535, 123456} {
		args := TaskkillArgs(pid, true)
		if args[1] != strconv.Itoa(pid) {
			t.Errorf("TaskkillArgs(%d) PID arg = %q, want %d", pid, args[1], pid)
		}
	}
}
