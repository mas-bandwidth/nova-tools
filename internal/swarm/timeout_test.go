package swarm

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTaskkillArgs verifies Windows taskkill argument construction.
func TestTaskkillArgs(t *testing.T) {
	t.Parallel()

	pid := 4242

	// Graceful tree termination (Phase 1 / SIGTERM equivalent)
	gracefulArgs := TaskkillArgs(pid, false)
	wantGraceful := []string{"/PID", "4242", "/T"}
	assert.Equal(t, wantGraceful, gracefulArgs, "TaskkillArgs(%d, false) = %v, want %v", pid, gracefulArgs, wantGraceful)

	// Forceful tree termination (Phase 2 / SIGKILL equivalent)
	forceArgs := TaskkillArgs(pid, true)
	wantForce := []string{"/PID", "4242", "/T", "/F"}
	assert.Equal(t, wantForce, forceArgs, "TaskkillArgs(%d, true) = %v, want %v", pid, forceArgs, wantForce)
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
	assert.True(t, hasTree, "taskkill forceful kill missing tree or force flags: %v", taskkillForceArgs)
	assert.True(t, hasForce, "taskkill forceful kill missing tree or force flags: %v", taskkillForceArgs)
}

// TestWindowsTreeReapFallback verifies the PID formatting of the taskkill fallback.
func TestWindowsTreeReapFallback(t *testing.T) {
	t.Parallel()

	// Verify PID string formatting for taskkill
	for _, pid := range []int{1, 100, 65535, 123456} {
		args := TaskkillArgs(pid, true)
		assert.Equal(t, strconv.Itoa(pid), args[1], "TaskkillArgs(%d) PID arg = %q, want %d", pid, args[1], pid)
	}
}
