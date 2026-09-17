package merge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CARD #83: efficiency card, 2026-09-12. The card is a measurement of what one
// nova-merge pass pays for, and its contract lives in the spec: one snapshot per
// pass, a coordinator read that is capped and counted, and a foreign lane
// refused rather than guessed. This doc test reads the section out of the spec
// the way TestCrossToolEfficiencyCardNamesItsRules reads its section: the spec
// is the one place the contract is written.
func TestNovaMergeEfficiencyCardNamesItsRules(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-MERGE.md"))
	if err != nil {
		t.Fatalf("the efficiency card's contract is the spec's: %s", err)
	}
	spec := string(raw)
	section := novaMergeEfficiencySection(t, spec)
	// The contract is prose, so its line wrapping is the spec's; collapse runs
	// of whitespace so a phrase is checked for its words, not its column.
	section = strings.Join(strings.Fields(section), " ")
	for _, want := range []string{
		// the measurement the card published, on the bench, 2026-09-12.
		"2026-09-12",
		"mas-bandwidth/schema",
		"26 entries",
		"0.49 s",
		"0.59 s",
		"28.1 s",
		"54 s",
		"two-minute rule",
		// the repeats rule: one snapshot per pass, never a cache of verdicts.
		"one snapshot per pass",
		"never re-derives in one pass what it already read",
		"not a cache of verdicts",
		"internal/merge/pass.go",
		"internal/merge/host.go",
		"internal/merge/classify.go",
		// the coordinator read: capped, counted, and the foreign lane refused.
		"bounded.Capped",
		"ONE PLACE COUNTS",
		"20 lines plus 2",
		"refusing to guess",
		"bin/merge-lane.sh",
		"lane.json",
		// the clock: the tool's own loop and the host's checks, and nothing else.
		"`run --loop <duration> --hours <h>` is the lane's clock",
		"`--timeout`",
		"Host.PR",
		"Host.Checks",
		// the Red tests list.
		"Red tests",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("SPEC-MERGE.md efficiency card names %q; the section holds:\n%s", want, section)
		}
	}
}

// novaMergeEfficiencySection returns the body of the `## The efficiency card
// (#83), 2026-09-12` section: the contract lives under that header and ends at
// the next top-level `## ` header.
func novaMergeEfficiencySection(t *testing.T, spec string) string {
	t.Helper()
	const header = "## The efficiency card (#83), 2026-09-12"
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
