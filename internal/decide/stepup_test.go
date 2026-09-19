package decide

import (
	"context"
	"strings"
	"testing"
)

// Edge 24: the step-up signal fires -- below the floor, exit 3 -- but the tool
// named only the question that fell below and never the rung above it, and
// nothing turned that signal into the next rung. Two things answer it: the line
// carries next=<rung>, and --step-up re-asks with the below-floor rung excluded
// from the criteria, every step a logged decision of its own.

// The line names where the work goes next when the answer is below the floor.
func TestRouteLineNamesTheNextRungBelowTheFloor(t *testing.T) {
	reg := testRegistry(t)
	u := Unit{ID: "thin", Kind: KindNewVerb} // thin evidence: confidence 0.60
	low, err := RouteRules(reg, u, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if low.Confidence >= DefaultFloor {
		t.Fatalf("this unit must fall below the floor to test the signal: %.2f", low.Confidence)
	}
	if low.Next == "" {
		t.Fatal("below the floor the decision must name the NEXT rung, not just the one that fell short")
	}
	if low.Next == low.Rung.Name {
		t.Errorf("next=%s is the rung already answered; the next rung is the one ABOVE it", low.Next)
	}
	if !strings.Contains(low.Line(), "next="+low.Next) {
		t.Errorf("the line does not carry the next rung: %s", low.Line())
	}
	if !strings.Contains(low.Line(), "steps=") {
		t.Errorf("every route line carries how many steps it took: %s", low.Line())
	}
	// At or above the floor there is no step to signal, and the field is the
	// dash every absent field on a line is.
	high := mustRoute(t, reg, Unit{ID: "sized", Kind: KindRebase, Files: 2, Packages: 1}, 0.5)
	if !strings.Contains(high.Line(), "next=-") {
		t.Errorf("above the floor there is no next rung to name: %s", high.Line())
	}
}

// --step-up re-asks the SAME question with the below-floor rung excluded from
// the criteria, up to maxSteps, and every step is a decision in its own right.
func TestRouteStepUpReAsksWithTheBelowFloorRungExcluded(t *testing.T) {
	reg := testRegistry(t)
	u := Unit{ID: "s-1", Kind: KindNewVerb, Files: 3, Packages: 1, Lanes: 1}
	unsure := &fakeDecider{choice: "rung-1", conf: 0.40}
	steps, err := RouteStepUp(context.Background(), unsure, reg, u, DefaultFloor, DefaultMaxSteps)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) < 2 {
		t.Fatalf("a below-floor answer re-asks: %d step(s)", len(steps))
	}
	if len(steps) > DefaultMaxSteps {
		t.Fatalf("--max-steps %d is a cap, got %d steps", DefaultMaxSteps, len(steps))
	}
	if unsure.calls != len(steps) {
		t.Errorf("%d steps but %d provider calls: every step is one asked decision", len(steps), unsure.calls)
	}
	seen := map[string]bool{}
	for i, s := range steps {
		if s.Steps != i+1 {
			t.Errorf("step %d carries steps=%d", i+1, s.Steps)
		}
		if seen[s.Rung.Name] {
			t.Errorf("step %d answered %s again; the rung below the floor is excluded from the criteria", i+1, s.Rung.Name)
		}
		seen[s.Rung.Name] = true
		if strings.TrimSpace(s.Reason) == "" {
			t.Errorf("step %d has no reason; every step is a logged decision with one", i+1)
		}
	}
	last := steps[len(steps)-1]
	if !strings.Contains(last.Line(), "steps=") {
		t.Errorf("the final line carries the step count: %s", last.Line())
	}
	// A cap of one is the old behaviour: ask once, and stop.
	one, err := RouteStepUp(context.Background(), &fakeDecider{choice: "rung-1", conf: 0.40}, reg, u, DefaultFloor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 {
		t.Errorf("--max-steps 1 asks once, got %d", len(one))
	}
}

// An answer at or above the floor is the answer: nothing steps.
func TestRouteStepUpStopsAtTheFloor(t *testing.T) {
	reg := testRegistry(t)
	u := Unit{ID: "s-2", Kind: KindNewVerb, Files: 3, Packages: 1, Lanes: 1}
	sure := &fakeDecider{choice: "rung-1", conf: 0.97}
	steps, err := RouteStepUp(context.Background(), sure, reg, u, DefaultFloor, DefaultMaxSteps)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || sure.calls != 1 {
		t.Fatalf("an above-floor answer is the answer: %d steps, %d calls", len(steps), sure.calls)
	}
	if steps[0].Confidence < DefaultFloor {
		t.Errorf("confidence = %.2f", steps[0].Confidence)
	}
}

// A max-steps that is not a count is a refusal, never a guess at one.
func TestRouteStepUpRefusesAMaxStepsThatIsNotACount(t *testing.T) {
	reg := testRegistry(t)
	u := Unit{ID: "s-3", Kind: KindRebase, Files: 2, Packages: 1}
	if _, err := RouteStepUp(context.Background(), &fakeDecider{choice: "rung-1", conf: 0.99}, reg, u, DefaultFloor, 0); err == nil {
		t.Error("--max-steps 0 asks nothing; it must be refused")
	}
}
