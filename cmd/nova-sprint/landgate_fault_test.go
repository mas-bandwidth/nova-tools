package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// classifyOutput classifies lander gate output: red for test failures, bench
// fault for infra problems.
func classifyOutput(out string) (red bool, faultKind string, what string) {
	out = strings.TrimSpace(out)
	if out == "" {
		return false, "", ""
	}
	// FAIL lines are red tree
	if strings.HasPrefix(out, "--- FAIL:") || strings.Contains(out, "FAIL\t") {
		return true, "", ""
	}
	// git errors are bench faults
	if strings.Contains(out, "exit status 128") || strings.Contains(out, "not a git repository") {
		return false, "git", strings.Split(out, "\n")[0]
	}
	// disk/quota errors are bench faults
	if strings.Contains(out, "ENOSPC") || strings.Contains(out, "disk quota exceeded") || strings.Contains(out, "no space left") {
		return false, "disk", strings.Split(out, "\n")[0]
	}
	// ssh errors are bench faults
	if strings.Contains(out, "exit status 255") {
		return false, "ssh", strings.Split(out, "\n")[0]
	}
	// copy/incomplete are bench faults
	if strings.Contains(out, "copy incomplete") || strings.Contains(out, "did not finish") {
		return false, "copy", strings.Split(out, "\n")[0]
	}
	return true, "", ""
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
		{"fault: not a git repo", "not a git repository", false, "git"},
		{"fault: ENOSPC", "build failed: ENOSPC", false, "disk"},
		{"fault: disk quota", "disk quota exceeded", false, "disk"},
		{"fault: no space left", "no space left on device", false, "disk"},
		{"fault: ssh exit 255", "ssh: connect to host example.com port 22: exit status 255", false, "ssh"},
		{"fault: copy incomplete", "copy incomplete: interrupted", false, "copy"},
		{"fault: did not finish", "stage did not finish", false, "copy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			red, kind, _ := classifyOutput(tt.out)
			assert.Equal(t, tt.wantRed, red)
			assert.Equal(t, tt.wantKind, kind)
		})
	}
}

// TestGateClassifiesNonRedAsRed tests that anything not clearly a bench fault is red.
func TestGateClassifiesNonRedAsRed(t *testing.T) {
	t.Parallel()
	out := "some other error"
	red, kind, _ := classifyOutput(out)
	assert.True(t, red)
	assert.Equal(t, "", kind)
}
