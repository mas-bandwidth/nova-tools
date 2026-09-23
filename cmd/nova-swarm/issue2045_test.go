package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// Issue #2045: launch: ONE launch verb replaces 27 launcher scripts (10 linux,
// 7 darwin twins, 5 Studio, rr/rrpro/rr-run): provider from a registry, any
// OS, local or ssh, output kept, no card lost.
//
// The core claim: a registry of providers exists; the label's hash selects
// which row runs. No script per provider. The test reads a one-row registry
// and asserts that `nova-swarm launch` names the provider and model the
// registry carries. On base-sha the verb is unknown (exit 2, "unknown
// subcommand").
func TestIssue2045(t *testing.T) {
	dir := t.TempDir()

	// One row: provider<TAB>model<TAB>harness.
	registry := filepath.Join(dir, "providers.tsv")
	write(t, registry, "flash\tanthropic/claude-sonnet-4-20250514\topencode\n")

	card := filepath.Join(dir, "card.md")
	write(t, card, "RESULT: c2045 launch verb\nKIND: fix\n")

	exit, stdout, stderr := runSwarm(t, "launch",
		"--providers", registry,
		"--label", "test-label",
		"--card", card,
	)

	// On base-sha the verb is unknown: exit 2, stderr names the verb.
	if exit == 2 && strings.Contains(stderr, "unknown subcommand") {
		t.Fatalf("nova-swarm launch not recognized (issue #2045); stderr: %s", stderr)
	}
	if exit != 0 {
		t.Fatalf("launch must exit 0, got %d:\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "LAUNCH OK") {
		t.Fatalf("launch must print LAUNCH OK:\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if !strings.Contains(stdout, "provider=flash") {
		t.Errorf("launch must name the selected provider:\n%s", stdout)
	}
	if !strings.Contains(stdout, "model=anthropic/claude-sonnet-4-20250514") {
		t.Errorf("launch must name the selected model:\n%s", stdout)
	}
}

// Determinism: the same label must always select the same provider row.
func TestIssue2045Deterministic(t *testing.T) {
	dir := t.TempDir()

	registry := filepath.Join(dir, "providers.tsv")
	write(t, registry, strings.Join([]string{
		"flash\tanthropic/claude-sonnet-4-20250514\topencode",
		"pro\topenai/gpt-4o\topencode",
		"ds\tdeepseek/deepseek-r1\topencode",
	}, "\n")+"\n")

	card := filepath.Join(dir, "card.md")
	write(t, card, "RESULT: c2045 deterministic\nKIND: fix\n")

	// Run twice with the same label; must select the same row.
	_, stdout1, _ := runSwarm(t, "launch",
		"--providers", registry,
		"--label", "same-label",
		"--card", card,
	)
	_, stdout2, _ := runSwarm(t, "launch",
		"--providers", registry,
		"--label", "same-label",
		"--card", card,
	)

	p1 := extractField(stdout1, "provider=")
	p2 := extractField(stdout2, "provider=")
	if p1 == "" || p2 == "" {
		t.Fatalf("both runs must carry provider=; first:\n%s\nsecond:\n%s", stdout1, stdout2)
	}
	if p1 != p2 {
		t.Errorf("same label must select same provider: first=%q second=%q", p1, p2)
	}
}

// extractField pulls the value of a field from a whitespace-separated line.
func extractField(line, prefix string) string {
	for _, f := range strings.Fields(line) {
		if strings.HasPrefix(f, prefix) {
			return strings.TrimPrefix(f, prefix)
		}
	}
	return ""
}
