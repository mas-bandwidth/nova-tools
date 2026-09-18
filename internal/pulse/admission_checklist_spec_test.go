package pulse

// SPEC-PULSE.md, "The learned admission checklist" (nova-tools #585, Ikrima 2026-09-16): a
// section nobody reads in a build rots, so this test reads it the way the decide spec test
// reads SPEC-DECIDE.md. Every rule the section numbers has a hurt that made it and a red
// test that holds it; a rule renamed out of the doc turns this red.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpecPulseNamesTheLearnedAdmissionChecklist(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-PULSE.md"))
	if err != nil {
		t.Fatalf("the pulse spec is missing: %s", err)
	}
	doc := string(raw)
	for _, phrase := range []string{
		"## The learned admission checklist",
		"cut from the abstain history, never invented",
		"runs the checklist against each new card before launch",
		"The five checks, each named for the class it was learned from",
		"names the known class and its remedy",
		"retained transfer",
		"the next decision survives it",
		"what the next decision needs",
		"not checked: <list>",
		"future capability per unit of total cost",
	} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("SPEC-PULSE.md does not name the rule keyed by %q", phrase)
		}
	}
	for _, red := range []string{
		"probe-reads-the-abstain-history",
		"probe-refuses-before-launch",
		"probe-names-the-five-checks",
		"probe-refusal-names-the-class",
		"probe-counts-by-class-per-day",
		"handoff-record-supports-next-decision",
		"cairn-preserves-next-decision",
		"read-result-states-not-checked",
		"ledger-counts-read-and-probe",
	} {
		if !strings.Contains(doc, red) {
			t.Errorf("SPEC-PULSE.md does not list the red test %q", red)
		}
	}
}

// The scope line lands in SPEC-REVIEW too, so a reader of a read record finds it where the
// review practice lives, not only in the cutter that writes it.
func TestSpecReviewNamesTheNotCheckedScope(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-REVIEW.md"))
	if err != nil {
		t.Fatalf("the review spec is missing: %s", err)
	}
	if !strings.Contains(string(raw), "not checked: <list>") {
		t.Error("SPEC-REVIEW.md does not carry the read RESULT scope line `not checked: <list>`")
	}
}
