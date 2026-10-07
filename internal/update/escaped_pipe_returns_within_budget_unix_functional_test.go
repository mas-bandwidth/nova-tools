//go:build !windows && functional

package update

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A child that spawns a grandchild holding stdout open in its own process group
// (so the group kill cannot reach it) and then hangs must still end inside the
// budget plus a small fixed slack: the deadline closes the held pipe instead of
// letting a fixed drain grace run past it.
func TestDeadlineEscapedPipeGrandchildReturnsInsideBudget(t *testing.T) {
	t.Parallel()

	ready := filepath.Join(t.TempDir(), "grandchild.ready")
	p := manifest(t, row("x", "tool", command(t, "escaped", "30s", ready), "npm:unused", "none"))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	readyCh := make(chan struct{})
	go func() {
		defer cancel()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if b, err := os.ReadFile(ready); err == nil && len(b) > 0 {
				pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
				if err == nil && pid > 0 {
					t.Cleanup(func() {
						_ = syscall.Kill(pid, syscall.SIGKILL)
					})
				}
				close(readyCh)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	var drainMu sync.Mutex
	var observedDrain time.Duration
	drainSeam := func(d time.Duration) (<-chan time.Time, func() bool) {
		drainMu.Lock()
		observedDrain = d
		drainMu.Unlock()
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch, func() bool { return true }
	}

	// The check blocks, so the event under test -- the call returning because
	// the deadline closed the held pipe, rather than a drain running past the
	// budget -- is read off a done channel, not the wall clock. A select
	// against the generous NOVA_TEST_WAIT bound (default 30s) is the allowed
	// poll (internal/ci/testdata/fixed-waits-allowlist.txt: "The allowed shape
	// is a poll up to NOVA_TEST_WAIT (default 30s) or a fake clock";
	// docs/SPEC-CI.md, waits: assert the event, not the clock).
	type checkResult struct {
		c    int
		out  string
		errs string
	}
	done := make(chan checkResult, 1)
	go func() {
		c, out, errs := run(t, Environment{Context: ctx, DrainTimer: drainSeam}, "check", "--file", p, "--budget", "30s", "--timeout", "2s")
		done <- checkResult{c: c, out: out, errs: errs}
	}()
	var c int
	var out, errs string
	select {
	case got := <-done:
		c, out, errs = got.c, got.out, got.errs
	case <-time.After(testWait()):
		require.Failf(t, "", "the check did not return within %s; an escaped grandchild must not hold the run past the budget", testWait())
	}

	select {
	case <-readyCh:
		// Readiness established before cancellation: verified that the escaped
		// pipe-holder was spawned and holding the pipe before timeout/cancellation.
	default:
		require.Failf(t, "", "escaped pipe-holder was not verified ready before exit; c=%d out=%s errs=%s", c, out, errs)
	}

	drainMu.Lock()
	drain := observedDrain
	drainMu.Unlock()
	if drain != 50*time.Millisecond {
		require.Failf(t, "", "drain allowance = %v, want 50ms (drainFloor)", drain)
	}

	if c != 1 {
		require.EqualValuesf(t, 1, c, "%d %s %s", c, out, errs)
	}
}
