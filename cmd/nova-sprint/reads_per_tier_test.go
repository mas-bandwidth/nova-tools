package main

import "testing"

// proBriefFile writes a passing brief whose line 1 names tier pro and returns
// its path, for add --brief-file: a pro card is read twice, by two different
// readers (sprint.ReadsNeeded), and a card with no brief is flash and read
// once. The tests of the two-reader flow admit pro cards.
func proBriefFile(t *testing.T) string {
	t.Helper()
	return writeBrief(t, "tier: pro")
}
