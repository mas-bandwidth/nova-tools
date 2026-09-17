package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CARD-8902 / nova-tools #81, nova-bus: efficiency card, 2026-09-12.
//
// The card is a MEASUREMENT, taken read-only against the live checkout on
// 2026-09-12, of the work nova-bus pays for twice and the one report that has
// no ceiling. Its contract lives in the spec, the way the rest of the
// efficiency-card set does: the git fetch behind every poll, the bounded
// `inbox` against the uncapped `check --full`, and the turn a wait costs
// whatever its length. This doc test reads the section out of docs/SPEC.md the
// way TestNovaCheckEfficiencyCardNamesItsRules reads #86's section: the spec is
// the one place the contract is written.
func TestNovaBusEfficiencyCardNamesItsRules(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC.md"))
	if err != nil {
		t.Fatalf("the nova-bus efficiency card's contract is the spec's: %s", err)
	}
	spec := string(raw)
	section := novaBusEfficiencySection(t, spec)
	// The contract is prose, so its line wrapping is the spec's; collapse runs
	// of whitespace so a phrase is checked for its words, not its column.
	section = strings.Join(strings.Fields(section), " ")
	for _, want := range []string{
		// the state the card measured against.
		"3,401 notes",
		"65 MB",
		"carrying=986",
		// the three measured operations, named as the card names them.
		"REPEATS: a git fetch per poll, and a whole-history walk on `check --full`",
		"COORDINATOR READ: bounded on `inbox`, unbounded on `check --full`",
		"WAITS ON: its own clock, and a quiet poll prints nothing",
		// REPEATS: the fetch and the measured poll budget.
		"0.98 s",
		"360 fetches",
		"5m 53s",
		"defaultWaitInterval",
		"cmd/nova-bus/main.go:2404",
		"cmd/nova-bus/main.go:2423",
		"cmd/nova-bus/main.go:2443",
		// REPEATS: the full walk against the cursor read.
		"342 lines",
		"76,616 B",
		"0.15 s",
		"135 B",
		"0.09 s",
		// COORDINATOR READ: the bounded inbox against the uncapped check.
		"693 B",
		"509 B",
		"INBOX OPEN carrying=986 heard=1",
		"12,035 B",
		"BUS WARN",
		"220-byte remedy",
		// WAITS ON: the turn a wait costs.
		"652M cache read",
		"1,204 turns",
		"542K cache-read tokens",
		// the missing bound and the flag that closes it.
		"`--fail-max`",
		"`--fail-max 0`",
		"BUS FINDING",
		"BUS MORE",
		"BUS SUMMARY",
		"`--after`",
		// the Red tests list.
		"Red tests",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("SPEC.md nova-bus efficiency card names %q; the section holds:\n%s", want, section)
		}
	}
}

// novaBusEfficiencySection returns the body of the `## The efficiency card
// (#81), nova-bus` section: the contract lives under that header and ends at
// the next top-level `## ` header.
func novaBusEfficiencySection(t *testing.T, spec string) string {
	t.Helper()
	const header = "## The efficiency card (#81), nova-bus"
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
