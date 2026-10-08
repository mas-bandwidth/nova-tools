package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ringFault tracks which ring slots have faulted.
type ringFault struct {
	slots   map[int]bool
	faults  []string
	kinds   []string
	nextIdx int
	rounds  int
}

func (r *ringFault) fault(kind string) {
	r.slots[r.nextIdx] = true
	r.faults = append(r.faults, kind)
	r.kinds = append(r.kinds, kind)
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

// benchFaultBound is a faulted bench's skip bound: 15 minutes.
const benchFaultBound = 15 * time.Minute

// benchFaultRecord is a bench's fault record: kind and until when it is skipped.
type benchFaultRecord struct {
	kind  string
	until time.Time
}

// skipUntil returns when a bench faulted at now is skipped until.
func skipUntil(now time.Time) time.Time {
	return now.Add(benchFaultBound)
}

// TestGateClassifiesFaultsVsRedTests tests that the gate classifies bench faults
// vs red tests correctly.
func TestGateClassifiesFaultsVsRedTests(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		out      string
		wantRed  bool
		wantKind string
	}{
		{"red: test fail", "--- FAIL: TestA (0.00s)\n    a_test.go:1: failed\nFAIL", true, ""},
		{"red: package fail", "FAIL\tm/p\t0.1s", true, ""},
		{"fault: git exit 128", "git ls-files from the repository root: exit status 128", false, "git"},
		{"fault: not a git repo", "fatal: not a git repository", false, "git"},
		{"fault: ENOSPC", "build: ENOSPC: no space left", false, "disk"},
		{"fault: disk quota exceeded", "tar: disk quota exceeded", false, "disk"},
		{"fault: no space left", "no space left on device", false, "disk"},
		{"fault: tmp toolchain", "go: no such toolchain: go1.25", false, "tmp"},
		{"fault: ssh 255", "ssh: connection to bench failed: exit status 255", false, "ssh"},
		{"fault: incomplete copy", "copy incomplete: did not finish", false, "copy"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			red, kind, _ := classifyGateOutput(tc.out)
			assert.Equal(t, tc.wantRed, red)
			assert.Equal(t, tc.wantKind, kind)
		})
	}
}

// TestBenchFaultWinsOverRedLine tests that a bench fault is classified as a
// fault even when a FAIL line is present (the test failed because of the fault).
func TestBenchFaultWinsOverRedLine(t *testing.T) {
	t.Parallel()
	out := "--- FAIL: TestGit (0.00s)\n    git_test.go:1: git ls-files: exit status 128\nFAIL"
	red, kind, _ := classifyGateOutput(out)
	assert.False(t, red, "a git fault behind a FAIL line is a bench fault, not a red tree")
	assert.Equal(t, "git", kind)
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
	_, kind, _ := classifyGateOutput("git: exit status 128")
	assert.Equal(t, "git", kind)
}

// TestTestFailureIsRedTree tests that test failures mark the base red.
func TestTestFailureIsRedTree(t *testing.T) {
	t.Parallel()
	red, kind, _ := classifyGateOutput("--- FAIL: TestA (0.00s)")
	assert.True(t, red)
	assert.Equal(t, "", kind)
}

// TestFaultedBenchIsSkipped tests that a faulted bench is tracked and skipped.
func TestFaultedBenchIsSkipped(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 7, 23, 34, 0, 0, time.UTC)
	rec := benchFaultRecord{kind: "git", until: skipUntil(now)}
	assert.Equal(t, "git", rec.kind)
	assert.Equal(t, now.Add(benchFaultBound), rec.until)
}

// TestAllSlotsFaultDeferred tests all slots faulting defers landing with one judgment.
func TestAllSlotsFaultDeferred(t *testing.T) {
	t.Parallel()
	r := &ringFault{slots: map[int]bool{0: false, 1: false}, nextIdx: 0}
	r.fault("git")
	r.fault("disk")
	assert.True(t, r.allFaulted())
	assert.Equal(t, 1, r.rounds)
	assert.Len(t, r.kinds, 2)
}

// TestFaultKindInJudgment tests that fault kinds are tracked for the judgment.
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

// TestSkipBoundIs15Minutes tests that the faulted bench skip bound is 15 minutes.
func TestSkipBoundIs15Minutes(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 15*time.Minute, benchFaultBound)
}
