package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SPEC CARD #880 ITEM 18 (docs/SPEC-SWARM.md, Bench slot leases): the bench
// slot lease section names its seven rules, the broker verbs, and its Red
// tests. This doc test reads the section out of the spec the way
// TestEveryRunLineParsesAgainstTheGrammar reads the Output grammar: the spec
// is the one place the contract is written.
func TestBenchSlotLeasesSectionNamesItsRules(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-SWARM.md"))
	if err != nil {
		t.Fatalf("the bench slot lease contract is the spec's: %s", err)
	}
	spec := string(raw)
	section := benchSlotLeasesSection(t, spec)
	for _, want := range []string{
		// the seven rules, numbered.
		"ONE slot store",
		"<bench store>/slots",
		"owner, pid, card label, until=",
		"takes a lease per card before it runs and releases it after",
		"nova-swarm slots take --store <dir> --owner <o> --n <k> --for <duration>",
		"<store>/shares.tsv",
		"refuses with the holder list when the share is spent",
		"never grants past capacity minus reserve",
		"nova-swarm slots list --store <dir>",
		"one line per lease",
		"a lease past until= whose pid is gone is reaped by the next take",
		"a pid alive past until= is DRIFT",
		"never reaped and never regranted",
		"a card found running under a root with no matching lease is DRIFT",
		"shares change only by a PR to the registry, never by a note",
		// the Red tests list.
		"Red tests",
		"two owners at their shares cannot exceed capacity",
		"an expired lease with a dead pid frees its slot",
		"an expired lease with a live pid is DRIFT and stays",
		"a launch without a lease is refused by the launcher",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("SPEC-SWARM.md Bench slot leases names %q; the section holds:\n%s", want, section)
		}
	}
}

// benchSlotLeasesSection returns the body of the `## Bench slot leases`
// section: the contract lives under that header and ends at the next
// top-level `## ` header.
func benchSlotLeasesSection(t *testing.T, spec string) string {
	t.Helper()
	const header = "## Bench slot leases"
	start := strings.Index(spec, header)
	if start < 0 {
		t.Fatalf("the spec has no %q section", header)
		return ""
	}
	rest := spec[start+len(header):]
	for _, line := range strings.SplitAfter(rest, "\n") {
		_ = line
		break
	}
	end := strings.Index(rest, "\n## ")
	if end < 0 {
		return rest
	}
	return rest[:end]
}
