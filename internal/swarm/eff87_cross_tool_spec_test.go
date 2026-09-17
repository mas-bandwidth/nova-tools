package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CARD #87, cross-tool: efficiency card, 2026-09-12. The card is a measurement
// of the same work paid for once per tool, and its contract lives in the spec:
// one reference checkout per batch behind a per-job `--reference`/`--dissociate`
// clone, and the prompt text the workers run owned by the tool's templates
// rather than a shell script. This doc test reads the section out of the spec
// the way TestBenchSlotLeasesSectionNamesItsRules reads the Bench slot leases
// section: the spec is the one place the contract is written.
func TestCrossToolEfficiencyCardNamesItsRules(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-SWARM.md"))
	if err != nil {
		t.Fatalf("the cross-tool efficiency card's contract is the spec's: %s", err)
	}
	spec := string(raw)
	section := crossToolEfficiencySection(t, spec)
	// The contract is prose, so its line wrapping is the spec's; collapse runs
	// of whitespace so a phrase is checked for its words, not its column.
	section = strings.Join(strings.Fields(section), " ")
	for _, want := range []string{
		// the measurement the card published, on the bench, 2026-09-12.
		"307 MB",
		"3.6M cache-read tokens",
		"21 jobs",
		// the clone rule: one reference checkout per batch, a per-job clone that
		// points at it and then dissociates.
		"one reference checkout per batch",
		"`--reference`",
		"`--dissociate`",
		"bin/child-clone.sh:111",
		// the prompt rule: the text the workers run is the tool's.
		"internal/swarm/templates.go",
		"the prompt text the workers run is the tool's",
		// the card is cross-tool: the shell scripts are prototypes, and live
		// state moves to the tool.
		"the shell scripts are prototypes",
		"No tool's live state is a shell script's private variable",
		// the Red tests list.
		"Red tests",
		"a per-job clone built with `--reference` and `--dissociate` shares the reference checkout's object graph",
		"the worker prompt carries the named template's conditions from the tool, with no shell script in the path",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("SPEC-SWARM.md cross-tool efficiency card names %q; the section holds:\n%s", want, section)
		}
	}
}

// crossToolEfficiencySection returns the body of the `## The efficiency card
// (#87), cross-tool` section: the contract lives under that header and ends at
// the next top-level `## ` header.
func crossToolEfficiencySection(t *testing.T, spec string) string {
	t.Helper()
	const header = "## The efficiency card (#87), cross-tool"
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
