package main

import (
	"strings"
	"testing"
)

func TestSprintAuditHelp(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runSprint("sprint", "audit", "-h")
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr: %q", code, stderr)
	}
	if !strings.Contains(stdout, "usage: nova-sprint sprint audit [flags]") {
		t.Errorf("stdout missing usage: %q", stdout)
	}
	if !strings.Contains(stdout, "--purge") {
		t.Errorf("stdout missing --purge flag: %q", stdout)
	}
	if !strings.Contains(stdout, "--redis") {
		t.Errorf("stdout missing --redis flag: %q", stdout)
	}
}

func TestSprintAuditTakesFlagsNotPositional(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runSprint("sprint", "audit", "extra_pos_arg")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if stdout != "" {
		t.Errorf("stdout not empty: %q", stdout)
	}
	if !strings.Contains(stderr, "takes flags, not positional arguments") {
		t.Errorf("stderr missing positional arg refusal: %q", stderr)
	}
	if !strings.Contains(stderr, "run: nova-sprint help") {
		t.Errorf("stderr missing help remedy: %q", stderr)
	}
}

func TestSprintAuditRefusesUnknownFamily(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runSprint("sprint", "audit", "--purge", "nonexistent_family", "--redis", "127.0.0.1:6379")
	if code != 2 {
		t.Fatalf("exit %d, want 2; stdout: %q, stderr: %q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout not empty: %q", stdout)
	}
	if !strings.Contains(stderr, "unknown family \"nonexistent_family\"") {
		t.Errorf("stderr missing unknown family refusal: %q", stderr)
	}
}
