package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestIssue1835 reproduces nova-tools #1835, measured 2026-09-19 on studio.local
// while dogfooding nova-merge as the user the docs would have us be. The bug is
// internal to the help banner: its USAGE block names every flag each verb takes
// (the banner is the entry point per docs/ONBOARDING.md lines 8-16), and the
// prose six lines below it says "No other verb takes --repo, --base or
// --lane-branch". Both halves reach the same reader; one of them must give.
//
// RUN: go test ./cmd/nova-merge/ -run TestIssue1835 -count=1
func TestIssue1835(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	if code := run([]string{"help"}, &out, &errOut, Deps{}); code != 0 {
		t.Fatalf("help: exit %d, want 0\nstderr: %s", code, errOut.String())
	}
	helpText := out.String()

	cases := []struct {
		verb string
		flag string
	}{
		{"simulate", "--repo"},
		{"simulate", "--base"},
		{"batch", "--repo"},
		{"land", "--repo"},
	}
	var named []string
	for _, c := range cases {
		line := extractHelpUsage(helpText, c.verb)
		if line == "" {
			t.Errorf("help banner has no usage line for %s", c.verb)
			continue
		}
		if !strings.Contains(line, c.flag) {
			t.Errorf("help banner's usage line for %s does not name %s (binary demands it on no-flag invocations): %q", c.verb, c.flag, line)
			continue
		}
		named = append(named, c.verb+" "+c.flag)
	}

	if strings.Contains(helpText, "No other verb takes --repo") {
		t.Errorf("help banner's prose contradicts its usage block: it claims `No other verb takes --repo` while %s already takes --repo per the usage block above. nova-tools #1835", strings.Join(named, ", "))
	}
	if strings.Contains(helpText, "No other verb takes --base") {
		t.Errorf("help banner's prose contradicts its usage block: it claims `No other verb takes --base` while simulate takes --base per the usage block above. nova-tools #1835")
	}
}

// extractHelpUsage returns the line of the help banner that begins with the usage
// block's pair-of-spaces indent and the verb's own name, exactly as a reader sees
// it. An empty answer means the verb is not in the help block, which is itself a
// detail the prose above contradicts on its own.
//
//	go:embed-free syntactic helper for a test.
func extractHelpUsage(helpText, verb string) string {
	needle := "  nova-merge " + verb + " "
	for _, line := range strings.Split(helpText, "\n") {
		if strings.HasPrefix(line, needle) {
			return line
		}
	}
	return ""
}
