package pulse

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProCardWaitsForPlanOK(t *testing.T) {
	dir := t.TempDir()

	if PlanFile != "PLAN.md" {
		t.Fatalf("PlanFile = %q, want PLAN.md per #2590", PlanFile)
	}
	planFile := filepath.Join(dir, PlanFile)

	ready, reason := PlanStepReady(dir, nil)
	if ready || reason != "plan file missing" {
		t.Fatalf("no plan: PlanStepReady = %v %q, want false \"plan file missing\"", ready, reason)
	}

	if err := os.WriteFile(planFile, []byte("Q: which approach? (A / B / C)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ready, reason = PlanStepReady(dir, []string{"Q: which approach? (A / B / C)"})
	if ready {
		t.Fatalf("PlanStepReady = true, want false: pro card must wait for PLAN-OK before executing")
	}
	if reason == "" {
		t.Fatal("PlanStepReady gave no reason; want a reason naming the missing PLAN-OK")
	}

	// PLAN-OK in prose, or not as the appended last line, does not approve.
	for _, plan := range []string{
		"1. wait for the friend to append PLAN-OK\n2. edit planstep.go\n",
		"PLAN-OK\n1. edit planstep.go\n",
		"step: write PLAN-OK when done\n",
	} {
		if err := os.WriteFile(planFile, []byte(plan), 0o644); err != nil {
			t.Fatal(err)
		}
		if ready, _ := PlanStepReady(dir, nil); ready {
			t.Fatalf("PlanStepReady = true for unapproved plan %q, want false", plan)
		}
	}

	if err := os.WriteFile(planFile, []byte("Q: which approach? (A / B / C)\nPLAN-OK\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ready, reason = PlanStepReady(dir, []string{"Q: which approach? (A / B / C)"})
	if !ready {
		t.Fatalf("PlanStepReady = false after PLAN-OK, want true: %s", reason)
	}

	// A card with no asks is not blocked by the ask rule.
	for _, asks := range [][]string{nil, {}} {
		if ready, reason := PlanStepReady(dir, asks); !ready {
			t.Fatalf("PlanStepReady(%v) = false, want true for a card with no asks: %s", asks, reason)
		}
	}

	for _, ask := range []string{
		"What should we do?",
		"which one ) (",
		"which one (see the notes) please",
		"which one (A)",
		"which one (A / )",
		"which one (A / (B) / C)",
	} {
		if ready, _ := PlanStepReady(dir, []string{ask}); ready {
			t.Fatalf("PlanStepReady = true for unbounded ask %q, want false: pro card asks must be bounded multiple choice", ask)
		}
	}
}
