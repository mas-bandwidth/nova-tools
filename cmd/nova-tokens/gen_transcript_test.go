package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestGenerateTranscript is the tool producing its own first-run transcript, so that
// TESTS.md is what the tool prints rather than what somebody remembered it printing. Run
// it with NOVA_TOKENS_TRANSCRIPT=1 and paste; it asserts nothing and is skipped otherwise.
func TestGenerateTranscript(t *testing.T) {
	if os.Getenv("NOVA_TOKENS_TRANSCRIPT") == "" {
		t.Skip("set NOVA_TOKENS_TRANSCRIPT=1 to print the first-run transcript")
	}
	fixtureIn(t)
	var doc strings.Builder
	for _, line := range []string{
		"fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts --bus ./bus",
		"check --out ./out",
		"sum --out ./out --month 2026-09",
	} {
		r := at(firstRunStamp).Do(t, strings.Fields(line)...)
		fmt.Fprintf(&doc, "$ nova-tokens %s\n%s%s[exit %d]\n\n", line, r.Stdout, r.Stderr, r.Code)
	}
	t.Log("\n" + doc.String())
}
