package decide

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SPEC-DECIDE.md is the typed-decision route's contract: nine numbered rules,
// each with the hurt that made it, and the red tests listed per adoption. A
// spec nobody reads in a build rots; this test reads it the way the swarm
// grammar test reads SPEC-SWARM.md, so a rule renamed out of the doc is red.
func TestSpecDecideNamesNineRules(t *testing.T) {
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
	} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("SPEC-DECIDE.md does not name the rule keyed by %q", phrase)
		}
	}
}
