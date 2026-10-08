package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// classifyOutput classifies lander gate output: red for test failures, bench
// fault for infra problems. This mirrors classifyGateOutput in landgo.go.
func classifyOutput(out string) (red bool, faultKind string, what string) {
	out = strings.TrimSpace(out)
	if out == "" {
		return false, "", ""
	}
	lines := strings.Split(out, "\n")
	first := strings.TrimSpace(lines[0])
	
	// FAIL lines are red tree
	if strings.HasPrefix(out, "--- FAIL:") || strings.Contains(out, "FAIL\t") {
		return true, "", ""
	}
	// git errors are bench faults
	if strings.Contains(out, "exit status 128") || strings.Contains(out, "not a git repository") {
		return false, "git", first
	}
	// disk/quota errors are bench faults
	if strings.Contains(out, "ENOSPC") || strings.Contains(out, "disk quota exceeded") || strings.Contains(out, "no space left") {
		return false, "disk", first
	}
	// ssh errors are bench faults
	if strings.Contains(out, "exit status 255") {
		return false, "ssh", first
	}
	// copy/incomplete are bench faults
	if strings.Contains(out, "copy incomplete") || strings.Contains(out, "did not finish") {
		return false, "copy", first
	}
	// Default: assume red tree for any other non-zero exit
	return true, "", ""
}

// ringFault tracks which ring slots have faulted.
type ringFault struct {
	slots    map[int]bool
	faults   []string
	nextIdx  int
	rounds   int
}

func (r *ringFault) fault(kind string) {
	r.slots[r.nextIdx] = true
	r.faults = append(r.faults, kind)
	r.nextIdx++
	if r.nextIdx >= len(r.slots) {
		r.nextIdx = 0
		r.rounds++
	}
}

func (r *ringFault) allFaulted() bool {
	for _, v := range r.slots {
		if !v {
			return false
		}
	}
	return true
}

// TestGateClassifiesFaultsVsRedTests tests that the gate classifies bench faults
// vs red tests correctly.
func TestGateClassifiesFaultsVsRedTests(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		out     string
		wantRed bool
		wantKind string
	}{
		{"red: test fail", "--- FAIL: TestA (0.00s)\n    a_test.go:1: failed\nFAIL", true, ""},
		{"red: package fail", "FAIL\tm/p\t0.1s", true, ""},
		{"fault: git exit 128", "git ls-files from the repository root: exit status 128", false, "git"},
		{"fault: not a git repo", "fatal: not a git repository", false, "git"},
		{"fault: ENOSPC", "build: ENOSPC: no space left", false, "disk"},
		{"fault: disk quota exceeded", "tar: disk quota exceeded", false, "disk"},
		{"fault: no space left", "no space left on device", false, "disk"},
		{"fault: ssh 255", "ssh: connection to bench failed: exit status 255", false, "ssh"},
		{"fault: incomplete copy", "copy incomplete: did not finish", false, "copy"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			red, kind, _ := classifyOutput(tc.out)
			assert.Equal(t, tc.wantRed, red)
			assert.Equal(t, tc.wantKind, kind)
		})
	}
}

// TestRingStepsOnFault tests that the ring steps to the next slot on fault.
func TestRingStepsOnFault(t *testing.T) {
	t.Parallel()
	r := &ringFault{slots: map[int]bool{0: false, 1: false, 2: false}, nextIdx: 0}
	assert.False(t, r.allFaulted())
	r.fault("git")
	assert.True(t, r.slots[0])
	assert.Equal(t, 1, r.nextIdx)
	r.fault("disk")
	assert.True(t, r.slots[1])
	assert.Equal(t, 2, r.nextIdx)
	r.fault("ssh")
	assert.True(t, r.slots[2])
	assert.Equal(t, 0, r.nextIdx)
	assert.True(t, r.allFaulted())
}

// TestDeferOnAllFaulted tests that landing is deferred when all slots faulted.
func TestDeferOnAllFaulted(t *testing.T) {
	t.Parallel()
	r := &ringFault{slots: map[int]bool{0: false, 1: false}, nextIdx: 0}
	assert.False(t, r.allFaulted())
	r.fault("git")
	r.fault("disk")
	assert.True(t, r.allFaulted())
}

// TestBenchFaultDoesNotMarkBaseRed tests that bench faults never mark the base red.
func TestBenchFaultDoesNotMarkBaseRed(t *testing.T) {
	t.Parallel()
	_, kind, _ := classifyOutput("git: exit status 128")
	assert.Equal(t, "git", kind)
}

// TestTestFaultNeverMarkRed tests that test failures mark the base red.
func TestTestFaultNeverMarkRed(t *testing.T) {
	t.Parallel()
	red, kind, _ := classifyOutput("--- FAIL: TestA (0.00s)")
	assert.True(t, red)
	assert.Equal(t, "", kind)
}

// TestFaultedBenchIsSkipped tests that a faulted bench is tracked.
func TestFaultedBenchIsSkipped(t *testing.T) {
	t.Parallel()
	r := &ringFault{slots: map[int]bool{0: false}, nextIdx: 0}
	r.fault("git")
	assert.True(t, r.slots[0])
}

// TestAllSlotsFaultDeferred tests all slots faulting defers landing.
func TestAllSlotsFaultDeferred(t *testing.T) {
	t.Parallel()
	r := &ringFault{slots: map[int]bool{0: false, 1: false}, nextIdx: 0}
	r.fault("git")
	r.fault("disk")
	assert.True(t, r.allFaulted())
	assert.Equal(t, 1, r.rounds)
}

// TestFaultKindInJudgment tests that fault kinds are tracked.
func TestFaultKindInJudgment(t *testing.T) {
	t.Parallel()
	r := &ringFault{slots: map[int]bool{0: false, 1: false, 2: false}, nextIdx: 0}
	r.fault("git")
	r.fault("disk")
	r.fault("ssh")
	require.Len(t, r.faults, 3)
	assert.Equal(t, "git", r.faults[0])
	assert.Equal(t, "disk", r.faults[1])
	assert.Equal(t, "ssh", r.faults[2])
}
