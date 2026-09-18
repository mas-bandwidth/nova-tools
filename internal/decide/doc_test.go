package decide

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SPEC-DECIDE.md is the typed-decision route's contract: twelve numbered rules,
// each with the hurt that made it, and the red tests listed per adoption. A
// spec nobody reads in a build rots; this test reads it the way the swarm
// grammar test reads SPEC-SWARM.md, so a rule renamed out of the doc is red.
func TestSpecDecideNamesTwelveRules(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-DECIDE.md"))
	if err != nil {
		t.Fatalf("the typed-decision route's spec is missing: %s", err)
	}
	doc := string(raw)
	for _, phrase := range []string{
		"a decision is a choice",
		"TypeSafe Jev",
		"nova-secrets exec",
		"public or synthetic material only",
		"below the floor is a suggestion",
		"confidence never authorizes",
		"decisions never write",
		"logged beside the outcome",
		"httptest fake",
		"ownership, budgets and leases",
		"carries an evidence pointer",
		"recorded as unknown",
		"a delivered batch or a finished task",
	} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("SPEC-DECIDE.md does not name the rule keyed by %q", phrase)
		}
	}
}

// The ladder of minds is an adoption of the route (Glenn 2026-09-18), and its
// contract lives in the same spec: the rungs, the floor that steps UP, the two
// kind designations, friends first, and the log the starting rung is
// regenerated from. A rule that is only in the code is a rule nobody can argue
// with, so this test reads the doc for each of them.
func TestSpecDecideNamesTheLadder(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-DECIDE.md"))
	if err != nil {
		t.Fatalf("the typed-decision route's spec is missing: %s", err)
	}
	doc := string(raw)
	for _, phrase := range []string{
		"the ladder of minds",
		"steps UP a rung, never down",
		"sideways before up",
		"the ladder is the retry policy",
		"Friends first",
		"is Johnny's always",
		"a fresh take",
		"rowan_pick",
		"regenerates the starting rung",
	} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("SPEC-DECIDE.md does not name the ladder rule keyed by %q", phrase)
		}
	}
	for _, kind := range Kinds {
		if !strings.Contains(doc, kind) && !strings.Contains(doc, strings.ReplaceAll(kind, "-", " ")) {
			t.Errorf("SPEC-DECIDE.md names no kind %q; the evidence's kinds are the log's key", kind)
		}
	}
}
