package swarm

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShellDenialReaderTellsTheFirstDenialAtOnce: callback fires once on first denial.
func TestShellDenialReaderTellsTheFirstDenialAtOnce(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var firedCount int
	var firedPath string
	var firedOnce sync.Once
	var doneCh chan struct{}

	r := NewShellDenialReader()
	r.callback = func(path string) {
		mu.Lock()
		defer mu.Unlock()
		firedOnce.Do(func() {
			firedPath = path
		})
		firedCount++
		if doneCh != nil {
			close(doneCh)
		}
	}

	lines := []string{
		"STEP 1 plan",
		"STEP 2 running",
		"/usr/bin/bash: line 1: /tmp/x: Permission denied",
		"some other output",
		"bash: /var/tmp/other: Permission denied",
	}

	doneCh = make(chan struct{})
	for _, line := range lines {
		_, err := r.Write([]byte(line + "\n"))
		require.NoError(t, err)
	}

	// Wait for callback to fire
	select {
	case <-doneCh:
		// Callback fired
	case <-time.After(30 * time.Second):
		t.Fatal("callback did not fire within 2 seconds")
	}

	// Callback should fire exactly once on first denial
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, firedCount, "callback should fire exactly once")
	assert.Equal(t, "/tmp/x", firedPath, "callback should fire on first denied path")
}

// TestShellDenialReasonGivesTheScratchRemedyForAWriteOutsideTheWall: /tmp paths get TMPDIR remedy.
func TestShellDenialReasonGivesTheScratchRemedyForAWriteOutsideTheWall(t *testing.T) {
	t.Parallel()

	// Test /tmp write - should get TMPDIR remedy
	d := ShellDenial{
		Path: "/tmp/test_stall.go",
		Step: "3",
		Line: "/usr/bin/bash: line 1: /tmp/test_stall.go: Permission denied",
	}

	reason := ShellDenialReason("a-card", "/jobs/a-card", "landlock", 1, d)
	assert.Contains(t, reason, "$TMPDIR", "reason should mention $TMPDIR remedy for /tmp write")
	assert.Contains(t, reason, "scratch files", "reason should mention scratch files")

	// Test denied exec under toolchain - should have read_roots candidate
	d2 := ShellDenial{
		Path: "/opt/sdk/tool/bin/go",
		Step: "3",
		Line: "/usr/bin/bash: line 1: /opt/sdk/tool/bin/go: Permission denied",
	}
	reason2 := ShellDenialReason("a-card", "/jobs/a-card", "landlock", 1, d2)
	assert.Contains(t, reason2, "read_roots", "reason should mention read_roots for toolchain path")
}
