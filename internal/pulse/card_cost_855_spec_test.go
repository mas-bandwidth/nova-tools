package pulse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CARD #855, nova-pulse: the card cost is context x harness turns. The measurement is
// 1,434.6M cache-read tokens against 62.1M input, and reasoning is 121% of the visible
// output. The contract -- the turn budget on cut, the per-card turn count on harvest, and
// the low reasoning setting on a read card -- lives in the spec the way the efficiency
// cards of #83, #84 and #85 live in theirs. This doc test reads the section out of
// SPEC-PULSE.md; the spec is the one place the contract is written.
func TestPulseCostCardNamesItsRules(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-PULSE.md"))
	if err != nil {
		t.Fatalf("the card cost contract is the spec's: %s", err)
	}
	spec := string(raw)
	section := pulseCostSection(t, spec)
	// The contract is prose, so its line wrapping is the spec's; collapse runs of
	// whitespace so a phrase is checked for its words, not its column.
	section = strings.Join(strings.Fields(section), " ")
	for _, want := range []string{
		// the measurement of 2026-09-16, over 1,068 jobs, all benches.
		"2026-09-16",
		"1,068 jobs",
		"62.1M",
		"7.2M",
		"4.0M",
		"1,434.6M",
		"8.7M",
		// the bill is context x harness turns, and the hurt is the re-sent context.
		"context x harness turns",
		"23x the input",
		"1.35M cache-read tokens",
		"121%",
		// rule 1: cut emits cards whose step count is the turn budget.
		"cut-steps-are-the-turn-budget",
		"at most 8 turns",
		"at most 20 turns",
		// rule 2: harvest records turns per card from the harness log.
		"harvest-records-turns-per-card",
		"harness.log",
		"template finding",
		// rule 3: the read card names the low reasoning setting.
		"cut-read-card-names-low-reasoning",
		"low reasoning setting",
		"reasoning_effort",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("SPEC-PULSE.md card cost names %q; the section holds:\n%s", want, section)
		}
	}
}

// pulseCostSection returns the body of the `## The cost of a card (#855), 2026-09-16`
// section: the contract lives under that header and ends at the next top-level `## `
// header.
func pulseCostSection(t *testing.T, spec string) string {
	t.Helper()
	const header = "## The cost of a card (#855), 2026-09-16"
	start := strings.Index(spec, header)
	if start < 0 {
		t.Fatalf("the spec has no %q section", header)
		return ""
	}
	rest := spec[start+len(header):]
	end := strings.Index(rest, "\n## ")
	if end < 0 {
		return rest
	}
	return rest[:end]
}
