package pulse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestLaunchGatesPlanModeCardAtAdmission is the plan step at the execution path (Stella's
// HOLD 6 on #3182: the harvest gate ran after the card had already executed). A pro card
// declaring `PLAN: PLAN.md` with no plan yet launches as a plan-only turn -- the batch
// carries a copy of the card with the PLAN-ONLY TURN instruction, not the card itself.
// With a PLAN.md that lacks PLAN-OK it is not launched at all. Once PLAN-OK is appended,
// the card itself is admitted to execute. Remove the planAdmitCards call from Launch and
// the first launch carries the card itself and the second launches it unapproved.
func TestLaunchGatesPlanModeCardAtAdmission(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarm(t, argvLog)
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	card := filepath.Join(src, "planned")
	if err := os.WriteFile(card, []byte("RESULT planned sha=000000000000\nREPO owner/repo\nPLAN: PLAN.md\nSTEP 1. go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cards := filepath.Join(root, "cards.tsv")
	if err := os.WriteFile(cards, []byte("planned\t-\tpro\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	launch := func(minute int) (int, string, string) {
		return runLaunch(t, LaunchInput{
			Cards: cards, Root: root, Slots: 4, Deadline: "120", Queue: true,
			Now: func() time.Time { return time.Date(2026, 9, 23, 12, minute, 0, 0, time.UTC) },
		})
	}
	batchCard := func(out string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(root, "cards", pulseID(t, out), "cards.tsv"))
		if err != nil {
			t.Fatal(err)
		}
		rows := nonEmptyLines(string(raw))
		if len(rows) != 1 {
			t.Fatalf("batch carried %d cards, want 1: %q", len(rows), raw)
		}
		f := strings.Split(rows[0], "\t")
		return f[len(f)-1]
	}

	// Turn one: no PLAN.md, so the launch is the plan-only turn.
	code, out, errb := launch(0)
	if code != 0 || !strings.Contains(out, "PULSE OK ") || !strings.Contains(out, "n=1") {
		t.Fatalf("plan turn: exit=%d stdout=%q stderr=%q, want one card launched", code, out, errb)
	}
	if !strings.Contains(errb, "PLAN TURN card=planned") {
		t.Fatalf("plan turn not named: %q", errb)
	}
	turn := batchCard(out)
	if turn == card {
		t.Fatalf("the plan-mode card itself was launched to execute with no approved plan")
	}
	raw, err := os.ReadFile(turn)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(string(raw), "\n"); lines[0] != "RESULT planned sha=000000000000" || !strings.HasPrefix(lines[1], PlanTurnHead) || !strings.Contains(string(raw), "PLAN: PLAN.md") {
		t.Fatalf("plan-only card = %q, want line 1 kept, the PLAN-ONLY TURN instruction next, the card after", raw)
	}

	// The plan is written but not approved: nothing launches.
	jobDir := filepath.Join(root, "0", "jobs", "planned")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := filepath.Join(jobDir, PlanFile)
	if err := os.WriteFile(plan, []byte("touch: internal/x/x.go\nQ: which symbol? (Foo / Bar)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errb = launch(1)
	if code != 0 || !strings.Contains(out, "PULSE PLAN admitted=0 refused=1") {
		t.Fatalf("unapproved plan: exit=%d stdout=%q stderr=%q, want PULSE PLAN admitted=0 refused=1", code, out, errb)
	}
	if !strings.Contains(errb, "ADMIT REFUSED card=planned gate=plan-step waiting for PLAN-OK") {
		t.Fatalf("the plan-step refusal was not named: %q", errb)
	}
	if raw, _ := os.ReadFile(argvLog); len(nonEmptyLines(string(raw))) != 1 {
		t.Fatalf("nova-swarm ran %d times, want only the plan turn: %q", len(nonEmptyLines(string(raw))), raw)
	}

	// The approve line: the card itself is admitted to execute.
	if err := os.WriteFile(plan, []byte("touch: internal/x/x.go\nQ: which symbol? (Foo / Bar)\nPLAN-OK\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errb = launch(2)
	if code != 0 || !strings.Contains(out, "n=1") {
		t.Fatalf("approved plan: exit=%d stdout=%q stderr=%q, want the card launched", code, out, errb)
	}
	if got := batchCard(out); got != card {
		t.Fatalf("approved launch carried %q, want the card itself %q", got, card)
	}
	if strings.Contains(errb, "PLAN TURN") || strings.Contains(errb, "ADMIT REFUSED") {
		t.Fatalf("approved launch still gated: %q", errb)
	}
}

// TestPlanAdmitPassesCardsNotInPlanMode: the negative control at admission -- a pro card
// with no PLAN: line and a flash card with one pass untouched, with no line printed.
func TestPlanAdmitPassesCardsNotInPlanMode(t *testing.T) {
	root := t.TempDir()
	noPlan := filepath.Join(root, "a.md")
	flash := filepath.Join(root, "b.md")
	if err := os.WriteFile(noPlan, []byte("RESULT a sha=aaa\nSTEP 1. go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flash, []byte("RESULT b sha=bbb\nPLAN: PLAN.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := []CardRow{{Label: "a", Slot: "-", Model: "pro", Card: noPlan}, {Label: "b", Slot: "-", Model: "flash", Card: flash}}
	var w strings.Builder
	got, refused, err := planAdmitCards(root, in, &w)
	if err != nil || refused != 0 || len(got) != 2 || got[0].Card != noPlan || got[1].Card != flash || w.Len() != 0 {
		t.Fatalf("planAdmitCards = %+v refused=%d err=%v out=%q, want both cards untouched and silent", got, refused, err, w.String())
	}
}
