package pulse

import (
	"os"
	"path/filepath"
	"strings"
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

// TestHarvestHoldsProCardUntilPlanOK is the production caller of PlanStepReady (Stella's
// HOLD 6 on #3165: the predicate had no caller, so a plan-mode card proceeded without the
// gate). A pro card whose card carries `PLAN: PLAN.md` finishes DONE with a red: line and
// a branch -- everything the red-line gate asks -- and harvest still HOLDS it while its
// PLAN.md lacks the PLAN-OK line: nothing is pushed. Appending PLAN-OK is the approve
// line; the next harvest folds the same job and pushes it. Remove the planHold call from
// Harvest and the first half fails (pushed=1 on an unapproved plan).
func TestHarvestHoldsProCardUntilPlanOK(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://example.invalid/owner/repo/pull/77")

	addCard(t, root, "planned", "1", "pro", "RESULT planned sha=ppp",
		"RESULT planned sha=ppp\nDONE\nBRANCH rowan/planned\nREPO owner/repo\nred: FAIL TestX\n")
	card := filepath.Join(root, "cardsrc", "planned.md")
	if err := os.WriteFile(card, []byte("RESULT planned sha=ppp\nREPO owner/repo\nPLAN: PLAN.md\nSTEP 1. go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := filepath.Join(root, "1", "jobs", "planned", PlanFile)
	if err := os.WriteFile(plan, []byte("touch: internal/x/x.go\nQ: which symbol? (Foo / Bar)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut := runHarvest(t, root)
	if strings.Contains(out, "pushed=1") {
		t.Fatalf("a plan-mode pro card was pushed with no PLAN-OK:\n%s\n%s", out, errOut)
	}
	if !strings.Contains(errOut, "HARVEST HOLD label=planned reason=plan-step") || !strings.Contains(errOut, "waiting for PLAN-OK") {
		t.Fatalf("the plan hold was not named:\n%s\n%s", out, errOut)
	}

	if err := os.WriteFile(plan, []byte("touch: internal/x/x.go\nQ: which symbol? (Foo / Bar)\nPLAN-OK\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errOut = runHarvest(t, root)
	if !strings.Contains(out, "pushed=1") {
		t.Fatalf("PLAN-OK did not resume the card (want pushed=1):\n%s\n%s", out, errOut)
	}
}

// TestPlanHoldSkipsCardsNotInPlanMode: a pro card with no PLAN: line and a flash card with
// one are not plan-gated (#2590: flash cards skip plan mode; plan mode is the card's own
// declaration, so pro cards cut before it keep folding as they did).
func TestPlanHoldSkipsCardsNotInPlanMode(t *testing.T) {
	dir := t.TempDir()
	noPlan := filepath.Join(dir, "a.md")
	withPlan := filepath.Join(dir, "b.md")
	if err := os.WriteFile(noPlan, []byte("RESULT a sha=aaa\nSTEP 1. go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(withPlan, []byte("RESULT b sha=bbb\nPLAN: PLAN.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if why, held := planHold(dir, CardRow{Model: "pro", Card: noPlan}); held {
		t.Fatalf("pro card without PLAN: line held: %s", why)
	}
	if why, held := planHold(dir, CardRow{Model: "flash", Card: withPlan}); held {
		t.Fatalf("flash card held by plan mode: %s", why)
	}
	if why, held := planHold(dir, CardRow{Model: "pro", Card: withPlan}); !held || why != "plan file missing" {
		t.Fatalf("pro plan-mode card with no PLAN.md: held=%v why=%q, want held \"plan file missing\"", held, why)
	}
}
