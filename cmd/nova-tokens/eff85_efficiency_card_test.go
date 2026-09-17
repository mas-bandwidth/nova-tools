package main

// CARD #85, nova-tokens: efficiency card, 2026-09-12. The card is a
// measurement of what the tool pays once and what it pays again, and its
// contract lives in the spec: one walk of the sources per run behind `--all`,
// a bounded coordinator read whose day line carries the shares, and a
// `--timeout` that bounds one source and not the run. This doc test reads the
// section out of the spec the way TestCrossToolEfficiencyCardNamesItsRules
// reads the #87 section: the spec is the one place the contract is written.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNovaTokensEfficiencyCardNamesItsRules(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-TOKENS.md"))
	if err != nil {
		t.Fatalf("the efficiency card's contract is the spec's: %s", err)
	}
	spec := string(raw)
	section := tokensEfficiencySection(t, spec)
	// The contract is prose, so its line wrapping is the spec's; collapse runs
	// of whitespace so a phrase is checked for its words, not its column.
	section = strings.Join(strings.Fields(section), " ")
	for _, want := range []string{
		// the measurement the card published, on the bench, 2026-09-12.
		"1,397",
		"1,699 MB",
		"57,239",
		"87 %",
		// the walk: claude.go parses the tree, the fold folds every day.
		"internal/tokens/claude.go:98",
		"cmd/nova-tokens/main.go:587",
		"one walk of the sources per run",
		"3.27 s",
		"dup=49765",
		"messages=57239",
		// the coordinator read: one source, one day, one OK, `check` one line
		// per finding under `--max`.
		"TOKENS SOURCE",
		"TOKENS DAY",
		"TOKENS OK",
		"`check` is one line per finding",
		"`--max`",
		"33",
		// what a run waits on: `--timeout` is one source, not the run.
		"120 s",
		"`--timeout`",
		"CHECK FAIL files=9 rows=0 first=2026-07-29 last=2026-09-11 bad=9 missing=36 stray=2",
		"`sum --month 2026-09`",
		"fold --day <d>",
		// the Red tests list.
		"Red tests",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("SPEC-TOKENS.md nova-tokens efficiency card names %q; the section holds:\n%s", want, section)
		}
	}
}

// tokensEfficiencySection returns the body of the `## The efficiency card
// (#85), nova-tokens` section: the contract lives under that header and ends at
// the next top-level `## ` header.
func tokensEfficiencySection(t *testing.T, spec string) string {
	t.Helper()
	const header = "## The efficiency card (#85), nova-tokens"
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
