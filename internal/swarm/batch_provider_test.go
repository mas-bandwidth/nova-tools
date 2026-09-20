package swarm

import (
	"os"
	"path/filepath"
	"testing"
)

// Issue #2011 & #2001: NATIVE PROVIDER verdict line recognition and batch scoring.

func TestIsNativeVerdictLineRecognizesProvider(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"NATIVE OK label=a", true},
		{"NATIVE INCOMPLETE label=b", true},
		{"NATIVE PROVIDER label=c why=unexpected-server-error", true},
		{"NATIVE PROVIDER label=d why=rate-limit ref=err_123", true},
		{"NATIVE REFUSED", false},
		{"RUNNER OK", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isNativeVerdictLine(tc.line); got != tc.want {
			t.Errorf("isNativeVerdictLine(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestScoreCardScoresNativeProviderWithRef(t *testing.T) {
	root := t.TempDir()
	job := filepath.Join(root, "1", "jobs", "a")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	line := "NATIVE PROVIDER label=a job=" + job + " tmp=/t rc=1 wall=1.00s sandbox=none-by-flag " +
		"card_sha256=aa binary_sha256=bb config=cc harness=ok why=unexpected-server-error ref=err_29c29bd4\n"
	if err := os.WriteFile(filepath.Join(job, "harness.log"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	state, reason, tail, _ := scoreCard(root, batchCard{label: "a", slot: 1, contract: "a card line 1"}, false, false, false, 1, 0, filepath.Join(root, "1", "native.log"), "")
	if state != "abstain" || reason != "provider" {
		t.Fatalf("state = %q, reason = %q, want abstain, provider", state, reason)
	}
	if tail != "why=unexpected-server-error ref=err_29c29bd4" {
		t.Errorf("tail = %q, want why=unexpected-server-error ref=err_29c29bd4", tail)
	}
}

func TestScoreCardScoresNativeProviderWithoutRef(t *testing.T) {
	root := t.TempDir()
	job := filepath.Join(root, "1", "jobs", "b")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	line := "NATIVE PROVIDER label=b job=" + job + " tmp=/t rc=1 wall=1.00s sandbox=none-by-flag " +
		"card_sha256=aa binary_sha256=bb config=cc harness=ok why=rate-limit\n"
	if err := os.WriteFile(filepath.Join(job, "harness.log"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	state, reason, tail, _ := scoreCard(root, batchCard{label: "b", slot: 1, contract: "b card line 1"}, false, false, false, 1, 0, filepath.Join(root, "1", "native.log"), "")
	if state != "abstain" || reason != "provider" {
		t.Fatalf("state = %q, reason = %q, want abstain, provider", state, reason)
	}
	if tail != "why=rate-limit" {
		t.Errorf("tail = %q, want why=rate-limit", tail)
	}
}
