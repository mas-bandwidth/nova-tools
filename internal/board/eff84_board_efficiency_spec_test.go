package board

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CARD #84, nova-board: efficiency card, 2026-09-12. The card is a measurement of what
// one verb costs, and its contract lives in the spec: one read and one append per verb
// with no loop and no interval, the coordinator read as counts and not cards, and
// `check` as the cheap gate. This doc test reads the section out of the spec the way
// TestCrossToolEfficiencyCardNamesItsRules reads the cross-tool card out of SPEC-SWARM.md:
// the spec is the one place the contract is written.
func TestBoardEfficiencyCardNamesItsRules(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-BOARD.md"))
	if err != nil {
		t.Fatalf("the board efficiency card's contract is the spec's: %s", err)
	}
	spec := string(raw)
	section := boardEfficiencySection(t, spec)
	// The contract is prose, so its line wrapping is the spec's; collapse runs of
	// whitespace so a phrase is checked for its words, not its column.
	section = strings.Join(strings.Fields(section), " ")
	for _, want := range []string{
		// the measurement the card published, on the bench, 2026-09-12.
		"0.44 s",
		"300 comments",
		"1.3 s per verb",
		"2.6 s per filing",
		"0.03 s",
		// the read rule and the two lines the card names it at.
		"THE READ IS THE WHOLE LOG",
		"internal/board/backend.go:8",
		"internal/board/issue.go:112",
		// one read and one append per verb, and no clock of its own.
		"one read and one append",
		"no loop and no interval",
		"There is no default duration",
		// the coordinator read is counts, not cards.
		"3 lines / 378 B",
		"330 B per card",
		"165 KB",
		// check is the cheap gate.
		"2 lines / 233 B",
		"exits 1 when it matched",
		// the Red tests list.
		"Red tests",
		"`list` on a board of many cards prints no `BOARD CARD` line",
		"every `--issue` verb fetches the thread once per invocation",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("SPEC-BOARD.md efficiency card names %q; the section holds:\n%s", want, section)
		}
	}
}

// boardEfficiencySection returns the body of the `## The efficiency card (#84),
// nova-board` section: the contract lives under that header and ends at the next
// top-level `## ` header.
func boardEfficiencySection(t *testing.T, spec string) string {
	t.Helper()
	const header = "## The efficiency card (#84), nova-board"
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
