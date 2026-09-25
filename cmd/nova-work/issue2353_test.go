package main

import (
	"os"
	"path/filepath"
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

// TestIssue2353ReportLineMatchesTheSpec pins the client half of SPEC-WORK.md's
// hand report (rules 7-11 of "The dependency gate and the hand report"): the
// help block carries the spec's `report` line from *The verbs* token for token,
// and every flag that line names (the six report flags and the write flags) is
// one this client forwards. The session-side refusals of rule 8 have no kernel
// yet, so this test pins only what the client carries.
func TestIssue2353ReportLineMatchesTheSpec(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-WORK.md"))
	if err != nil {
		t.Fatalf("read docs/SPEC-WORK.md: %v", err)
	}
	var specLine string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(l, "nova-work report ") {
			specLine = l
			break
		}
	}
	if specLine == "" {
		t.Fatal("docs/SPEC-WORK.md carries no `nova-work report` line in The verbs")
	}
	var helpLine string
	for _, l := range strings.Split(usage, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "nova-work report ") {
			helpLine = strings.TrimSpace(l)
			break
		}
	}
	if helpLine == "" {
		t.Fatal("the help block carries no `nova-work report` line")
	}
	if got, want := strings.Fields(helpLine), strings.Fields(specLine); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("help report line differs from the spec:\nhelp: %s\nspec: %s", helpLine, specLine)
	}

	carried := map[string]bool{}
	for _, f := range moreVerbFlags["report"] {
		carried[f.name] = true
	}
	for _, name := range []string{"session", "as", "request", "expect", "now", "deadline", "dry-run",
		"act", "subject", "what", "acted-at", "instead-of", "reason"} {
		if !carried[name] {
			t.Errorf("report does not carry --%s", name)
		}
	}
	for _, tok := range strings.Fields(specLine) {
		if strings.HasPrefix(tok, "--") && !carried[strings.TrimPrefix(tok, "--")] {
			t.Errorf("the spec's report line names %s, which the client does not carry", tok)
		}
	}
}

// TestIssue2353ReportRefusesAnUnknownFlagAtTwo pins that report is a carried
// verb with its own flag table: a flag outside it is exit 2 before any dial.
func TestIssue2353ReportRefusesAnUnknownFlagAtTwo(t *testing.T) {
	code, stdout, stderr := invoke("report", "--session", "missing.sock", "--as", "Rowan", "--bogus", "x")
	if code != 2 {
		t.Fatalf("want exit 2, got %d (stdout=%q stderr=%q)", code, stdout, stderr)
	}
	if strings.Contains(stderr, "SPEC-AHEAD") || strings.Contains(stderr, "no such session") {
		t.Fatalf("an unknown flag was not refused by the flag table: %q", stderr)
	}
}
