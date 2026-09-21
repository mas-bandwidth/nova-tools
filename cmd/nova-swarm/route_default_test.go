package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// H4 (SPEC-DECIDE, issue #1625): routing is the launcher's default and the one
// way out is an explicit --no-route carrying a --reason. A launcher that names
// neither must route; a launcher that names --no-route without a reason must be
// refused by name, because a skipped route is a fact in the log and not an
// absence.
func TestLaunchCannotSkipTheRouteWithoutALoggedReason(t *testing.T) {
	// Routing on with no accounting home is a rule answer, not a refusal: a
	// call nobody can account for is not made, but the card is still routed.
	f := newFlags("batch")
	var note strings.Builder
	if in := routeInput(f, routeFlags{on: true}, &note); in == nil {
		t.Fatalf("routing with no accounting flags must answer by the rules and say why=no-accounting, not refuse: %s", note.String())
	}

	// --no-route with no --reason is refused, and the refusal names --reason.
	dir := t.TempDir()
	card := filepath.Join(dir, "card.md")
	if err := os.WriteFile(card, []byte("RESULT: c1 nova-tools rebase onto dev\nKIND: rebase\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cards := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(cards, []byte("c1\t1\tvendor/low-1\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, _, stderr := runSwarm(t, "batch", "--id", "b1", "--cards", cards,
		"--deadline", "60", "--runner", "true", "--root", dir, "--files", "1", "--no-route")
	if exit != 2 || !strings.Contains(stderr, "--reason") {
		t.Fatalf("--no-route without --reason must be refused by name (exit 2 naming --reason), got exit=%d stderr=%q", exit, stderr)
	}
}
