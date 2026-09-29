package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SPEC CARD #80 (docs/SPEC-SWARM.md, Efficiency: lessons absorbed):
// the card is one of the seven-tool efficiency cards and records two measured
// operations of nova-swarm -- the per-job clone and the waits a job holds -- as
// normative contract (status and triage measurements were excised with the dead
// pool verbs). This doc test reads the section out of the spec the way
// TestBenchSlotLeasesSectionNamesItsRules does: the spec is the one place the
// contract is written.
func TestEfficiencyCardSectionNamesItsTwoMeasuredOperations(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-SWARM.md"))
	if err != nil {
		t.Fatalf("the efficiency contract is the spec's: %s", err)
	}
	section := efficiencySection(t, string(raw))
	for _, want := range []string{
		// The two measured operations, named as the card names them.
		"REPEATS: one full clone of the repository per job",
		"WAITS ON: a deadline, a sampler, and a person",
		// REPEATS: the shape the card points at.
		"--reference-if-able",
		"--dissociate",
		"86,794 cache-read tokens per tool call",
		// WAITS ON: the deadline and the sampler.
		"--deadline",
		"--usage-interval",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("SPEC-SWARM.md Efficiency: lessons absorbed names %q; the section holds:\n%s", want, section)
		}
	}
}

// efficiencySection returns the body of the `## Efficiency: lessons absorbed`
// section: the contract lives under that header and ends at the next
// top-level `## ` header.
func efficiencySection(t *testing.T, spec string) string {
	t.Helper()
	const header = "## Efficiency: lessons absorbed"
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
