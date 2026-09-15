package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The grammar block in docs/SPEC-REVIEW.md lists one line per event for seven
// verbs. The binary ships `packet`, `version` and `help`; the other six
// (`verdict`, `answer`, `policy`, `roster`, `dedupe`, `cost`) are not built,
// and the spec strikes their output lines from the grammar with `~~…~~` until
// they are. This test is what catches it the moment a struck line is unread as
// live output, or a live line drifts unbuilt: the grammar and the dispatch
// (`main.go`'s switch) cannot disagree about which verbs ship.
func TestGrammarStrikesTheUnbuiltVerbs(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-REVIEW.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(doc), "One machine-scannable line per event")
	if !ok {
		t.Fatal("the spec has no one-line-per-event grammar")
	}
	_, block, ok := strings.Cut(after, "```")
	if !ok {
		t.Fatal("the grammar section has no fenced block")
	}
	block, _, ok = strings.Cut(block, "```")
	if !ok {
		t.Fatal("the grammar block does not close")
	}

	shipped := map[string]bool{"PACKET": true}
	unbuilt := map[string]bool{
		"VERDICT": true, "ANSWER": true, "POLICY": true,
		"ROSTER": true, "DEDUPE": true, "COST": true,
	}

	for _, line := range strings.Split(strings.TrimRight(block, "\n"), "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "~~") && !strings.HasSuffix(line, "~~") {
			t.Errorf("grammar line is half-struck (opens `~~`, does not close): %q", line)
			continue
		}
		struck := strings.HasPrefix(line, "~~") && strings.HasSuffix(line, "~~")
		if struck {
			line = strings.TrimSuffix(strings.TrimPrefix(line, "~~"), "~~")
		}
		verb, _, _ := strings.Cut(line, " ")
		switch {
		case shipped[verb]:
			if struck {
				t.Errorf("`%s` ships, but its grammar line is struck: %q", verb, line)
			}
		case unbuilt[verb]:
			if !struck {
				t.Errorf("`%s` is not built, but its grammar line is unstruck: %q", verb, line)
			}
		default:
			t.Errorf("grammar line names an unknown verb %q: %q", verb, line)
		}
	}
}
