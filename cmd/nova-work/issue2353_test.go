package main

import (
	"strings"
	"testing"
)

// TestIssue2353Repro pins nova-tools #2353: the report verb is part of the
// client's socket-verb table. On base it is refused as SPEC-AHEAD; once the
// client carries it, a well-formed report reaches the session dial and is
// refused there as a missing session, not as an unknown or ahead verb.
func TestIssue2353(t *testing.T) {
	code, stdout, stderr := invoke(
		"report",
		"--session", "missing.sock",
		"--as", "Rowan",
		"--act", "launched",
		"--subject", "node:n1",
		"--what", "started worker",
		"--acted-at", "2026-09-23T00:00:00Z",
		"--instead-of", "-",
		"--reason", "hand launch",
	)
	if code != 2 {
		t.Fatalf("want exit 2, got %d (stdout=%q stderr=%q)", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("report wrote stdout on a missing session: %q", stdout)
	}
	line := strings.TrimSuffix(stderr, "\n")
	if strings.Contains(line, "SPEC-AHEAD") {
		t.Fatalf("report is still refused as SPEC-AHEAD: %q", line)
	}
	if !strings.Contains(line, "no such session") {
		t.Fatalf("report did not reach the session dial: %q", line)
	}
}
