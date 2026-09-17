package swarm

import (
	"strings"
	"testing"
)

// SPEC-SWARM, issue #856. A card whose STEPs exceed the pipeline bound of three
// model calls and that does not declare `MODE: explore` is refused with the
// remedy line, at admission, before any worker starts. The remedy names the
// keyword because a refusal a caller cannot act on is a refusal wasted.
func TestAdmissionRefusesAFourthCallWithoutExplore(t *testing.T) {
	four := "RESULT: fix the rule\nSTEP 1 read the test\nSTEP 2 write the red test\nSTEP 3 fix the source\nSTEP 4 write the result\n"
	why := admitWhyOf(t, t.TempDir(), "p4", "opencode/deepseek-v4-flash", four)
	if why == "" {
		t.Fatal("a card naming a fourth model call without `MODE: explore` is refused, got none")
	}
	if !strings.Contains(why, "MODE: explore") {
		t.Fatalf("the remedy names the keyword, got %q", why)
	}
	if !strings.Contains(why, "a card is a pipeline") {
		t.Fatalf("the remedy names the rule, got %q", why)
	}
	// The same card with `MODE: explore` is admitted: the loop is the exception,
	// and the exception is a word the card says.
	explore := "MODE: explore\n" + four
	if why := admitWhyOf(t, t.TempDir(), "p5", "opencode/deepseek-v4-flash", explore); why != "" {
		t.Fatalf("an explore card is admitted, got %q", why)
	}
}

// A card that stops at the pipeline bound needs no mode word: three calls are
// the shape, not the exception.
func TestTheFixCardRunsInThreeModelCalls(t *testing.T) {
	three := "RESULT: fix the rule\nSTEP 1 write the red test\nSTEP 2 write the fix\nSTEP 3 write the result\n"
	if why := admitWhyOf(t, t.TempDir(), "p3", "opencode/deepseek-v4-flash", three); why != "" {
		t.Fatalf("a three-step card is admitted, got %q", why)
	}
	if n := highestModelStep(three); n != 3 {
		t.Fatalf("a three-step card's highest call is 3, got %d", n)
	}
}

// The explore card carries a turn budget the harness enforces, and the budget is
// read from the card so the stop and the report can both name it.
func TestExploreOverTurnBudgetIsStoppedWithTheBudgetNamed(t *testing.T) {
	card := "MODE: explore\nTURNS: 4\nRESULT: find where the rule lives\nSTEP 1 grep\n"
	n, ok := exploreTurnBudget(card)
	if !ok || n != 4 {
		t.Fatalf("an explore card's turn budget is read from the card, got %d,%v", n, ok)
	}
	if _, ok := exploreTurnBudget("RESULT: fix\nSTEP 1 go\n"); ok {
		t.Fatal("a card with no `MODE: explore` has no turn budget")
	}
}
