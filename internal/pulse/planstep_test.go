package pulse

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProCardWaitsForPlanOK(t *testing.T) {
	dir := t.TempDir()

	planFile := filepath.Join(dir, PlanFile)
	if err := os.WriteFile(planFile, []byte("Q: which approach? (A / B / C)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ready, reason := PlanStepReady(dir, []string{"Q: which approach? (A / B / C)"})
	if ready {
		t.Fatalf("PlanStepReady = true, want false: pro card must wait for PLAN-OK before executing")
	}
	if reason == "" {
		t.Fatal("PlanStepReady gave no reason; want a reason naming the missing PLAN-OK")
	}

	if err := os.WriteFile(planFile, []byte("PLAN-OK\nQ: which approach? (A / B / C)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ready, reason = PlanStepReady(dir, []string{"Q: which approach? (A / B / C)"})
	if !ready {
		t.Fatalf("PlanStepReady = false after PLAN-OK, want true: %s", reason)
	}

	ready, _ = PlanStepReady(dir, []string{"What should we do?"})
	if ready {
		t.Fatal("PlanStepReady = true for unbounded ask, want false: pro card asks must be bounded multiple choice")
	}
}
