package pulse

import (
	"strings"
	"testing"
)

func TestCardTemplateV2A1InlineEvidence(t *testing.T) {
	// A1: Inline evidence (prior diff, reviewer line, failing test output).
	// Must cap diff at 6 KB (6144 bytes) and report omitted bytes.
	shortDiff := "--- a/foo.go\n+++ b/foo.go\n@@ -1 +1 @@\n-old\n+new\n"
	revLine := "DISPOSITION who=Rowan verdict=HOLD head=abc12345 reason=missing test for boundary"
	failingOut := "--- FAIL: TestBoundary (0.01s)\n    boundary_test.go:42: expected error, got nil\nFAIL"

	card, err := RenderCardV2(CardV2Input{
		Kind:          "recut",
		Number:        101,
		Repo:          "mas-bandwidth/nova-tools",
		Title:         "fix boundary handling in cut",
		Branch:        "rowan/fix-boundary",
		Base:          "dev",
		Location:      "internal/pulse/cut.go:42",
		TestPackage:   "./internal/pulse",
		TestFunction:  "TestBoundary",
		TestCommand:   "go test ./internal/pulse -run TestBoundary",
		Paths:         "internal/pulse/cut.go internal/pulse/cut_test.go",
		ReviewerLine:  revLine,
		PriorDiff:     shortDiff,
		FailingOutput: failingOut,
	})
	if err != nil {
		t.Fatalf("RenderCardV2 failed: %v", err)
	}

	if !strings.Contains(card, revLine) {
		t.Errorf("card does not contain reviewer line:\n%s", card)
	}
	if !strings.Contains(card, shortDiff) {
		t.Errorf("card does not contain inlined prior diff:\n%s", card)
	}
	if !strings.Contains(card, failingOut) {
		t.Errorf("card does not contain failing test output:\n%s", card)
	}
	if strings.Contains(card, "the prior diff is in the mirror, run git diff") {
		t.Errorf("card points away to mirror instead of inlining evidence")
	}

	// Test diff capping at 6 KB
	largeDiff := strings.Repeat("+// added line for large diff testing\n", 300) // ~11 KB
	cappedCard, err := RenderCardV2(CardV2Input{
		Kind:         "recut",
		Number:       102,
		Repo:         "mas-bandwidth/nova-tools",
		Title:        "large diff recut",
		Branch:       "rowan/large-diff",
		Base:         "dev",
		Location:     "internal/pulse/cut.go:1",
		TestPackage:  "./internal/pulse",
		TestFunction: "TestBoundary",
		TestCommand:  "go test ./internal/pulse -run TestBoundary",
		Paths:        "internal/pulse/cut.go",
		PriorDiff:    largeDiff,
	})
	if err != nil {
		t.Fatalf("RenderCardV2 failed with large diff: %v", err)
	}
	if !strings.Contains(cappedCard, "[inlined diff capped at 6144 bytes; omitted") {
		t.Errorf("large diff was not capped with omission notice:\n%s", cappedCard)
	}
}

func TestCardTemplateV2A2NamedPlaceAndTestCommand(t *testing.T) {
	// A2: Named place and one test command. PATHS as declared scope.
	card, err := RenderCardV2(CardV2Input{
		Kind:         "fix",
		Number:       103,
		Repo:         "mas-bandwidth/nova-tools",
		Title:        "null pointer on empty queue",
		Branch:       "emma/fix-nil-queue",
		Base:         "dev",
		Location:     "internal/pulse/queue.go:88",
		TestPackage:  "./internal/pulse",
		TestFunction: "TestEmptyQueueDoesNotPanic",
		TestCommand:  "go test ./internal/pulse -run TestEmptyQueueDoesNotPanic",
		Paths:        "internal/pulse/queue.go internal/pulse/queue_test.go",
	})
	if err != nil {
		t.Fatalf("RenderCardV2 failed: %v", err)
	}

	for _, want := range []string{
		"LOCATION: internal/pulse/queue.go:88",
		"TEST: ./internal/pulse.TestEmptyQueueDoesNotPanic",
		"COMMAND: go test ./internal/pulse -run TestEmptyQueueDoesNotPanic",
		"PATHS: internal/pulse/queue.go internal/pulse/queue_test.go",
		"Read scope: Do not read outside PATHS and the test's package.",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("card missing expected named place / command element %q:\n%s", want, card)
		}
	}
}

func TestCardTemplateV2A3PreflightLine(t *testing.T) {
	// A3: Preflight line in card.
	card, err := RenderCardV2(CardV2Input{
		Kind:         "fix",
		Number:       104,
		Repo:         "mas-bandwidth/nova-tools",
		Title:        "preflight check card",
		Branch:       "emma/preflight-card",
		Base:         "dev",
		Location:     "internal/pulse/cut.go:10",
		TestPackage:  "./internal/pulse",
		TestFunction: "TestCut",
		TestCommand:  "go test ./internal/pulse -run TestCut",
		Paths:        "internal/pulse/cut.go",
		PreflightCmd: "make preflight",
	})
	if err != nil {
		t.Fatalf("RenderCardV2 failed: %v", err)
	}

	if !strings.Contains(card, "make preflight") {
		t.Errorf("card does not contain preflight command:\n%s", card)
	}
	if !strings.Contains(card, "Before writing RESULT.md, run the class-rule preflight on the current tip") {
		t.Errorf("card does not contain preflight instruction before RESULT.md:\n%s", card)
	}
}

func TestCardTemplateV2A4PerKindTemplatesAndTurnBudgets(t *testing.T) {
	// A4: Per-kind templates and turn budgets: recut, fix, port, docs-guard, report, read.
	expectedBudgets := map[string]int{
		"read":       8,
		"docs-guard": 10,
		"report":     10,
		"port":       15,
		"recut":      15,
		"fix":        20,
	}

	for kind, wantBudget := range expectedBudgets {
		if got := TurnBudgetV2(kind); got != wantBudget {
			t.Errorf("kind %s turn budget = %d, want %d", kind, got, wantBudget)
		}

		card, err := RenderCardV2(CardV2Input{
			Kind:         kind,
			Number:       200,
			Repo:         "mas-bandwidth/nova-tools",
			Title:        "testing kind " + kind,
			Branch:       "worker/test-" + kind,
			Base:         "dev",
			Location:     "foo.go:1",
			TestPackage:  "./pkg",
			TestFunction: "TestFoo",
			TestCommand:  "go test ./pkg -run TestFoo",
			Paths:        "foo.go",
		})
		if err != nil {
			t.Fatalf("RenderCardV2 failed for kind %s: %v", kind, err)
		}

		wantTurnStr := strings.ToLower(card)
		if !strings.Contains(wantTurnStr, "turn budget:") && !strings.Contains(wantTurnStr, "turns:") {
			t.Errorf("card for kind %s does not specify turn budget:\n%s", kind, card)
		}
	}
}

func TestCardTemplateV2A5NoQuotedPRBodiesOrFooters(t *testing.T) {
	// A5: Strip noise: no quoted PR bodies, footers, or harvest paths.
	noisyTitle := "clean title"
	card, err := RenderCardV2(CardV2Input{
		Kind:         "fix",
		Number:       105,
		Repo:         "mas-bandwidth/nova-tools",
		Title:        noisyTitle,
		Branch:       "emma/clean-card",
		Base:         "dev",
		Location:     "internal/pulse/cut.go:50",
		TestPackage:  "./internal/pulse",
		TestFunction: "TestClean",
		TestCommand:  "go test ./internal/pulse -run TestClean",
		Paths:        "internal/pulse/cut.go",
	})
	if err != nil {
		t.Fatalf("RenderCardV2 failed: %v", err)
	}

	for _, banned := range []string{
		"Signed-off-by",
		"Co-authored-by",
		"Closes #",
		"Harvested from",
		"PR body:",
		"Pull request body:",
	} {
		if strings.Contains(card, banned) {
			t.Errorf("card contains noisy PR body/footer text %q:\n%s", banned, card)
		}
	}
}

func TestCardTemplateV2A6ThreeClausesPerKind(t *testing.T) {
	// A6: Three clauses per kind in conditions block.
	kinds := []string{"recut", "fix", "port", "docs-guard", "report", "read"}

	for _, k := range kinds {
		clauses := KindClauses(k)
		if len(clauses) != 3 {
			t.Errorf("kind %s has %d clauses, want exactly 3", k, len(clauses))
		}
		for i, c := range clauses {
			if strings.TrimSpace(c) == "" {
				t.Errorf("kind %s clause %d is empty", k, i+1)
			}
		}

		card, err := RenderCardV2(CardV2Input{
			Kind:         k,
			Number:       300,
			Repo:         "mas-bandwidth/nova-tools",
			Title:        "clause test " + k,
			Branch:       "worker/clause-" + k,
			Base:         "dev",
			Location:     "foo.go:1",
			TestPackage:  "./pkg",
			TestFunction: "TestFoo",
			TestCommand:  "go test ./pkg -run TestFoo",
			Paths:        "foo.go",
		})
		if err != nil {
			t.Fatalf("RenderCardV2 failed for kind %s: %v", k, err)
		}

		// Ensure card contains the 3 clauses
		for i, c := range clauses {
			if !strings.Contains(card, c) {
				t.Errorf("card for %s does not contain clause %d (%q):\n%s", k, i+1, c, card)
			}
		}
	}
}

func TestCardTemplateV2A7ResultTemplateAndExemplar(t *testing.T) {
	// A7: RESULT.md fill-in template with one exemplar per kind.
	// Preserves DONE, ABSTAIN, BLOCKED; separate typed check conclusion.
	kinds := []string{"recut", "fix", "port", "docs-guard", "report", "read"}

	for _, k := range kinds {
		card, err := RenderCardV2(CardV2Input{
			Kind:         k,
			Number:       400,
			Repo:         "mas-bandwidth/nova-tools",
			Title:        "exemplar test " + k,
			Branch:       "worker/exemplar-" + k,
			Base:         "dev",
			Location:     "foo.go:1",
			TestPackage:  "./pkg",
			TestFunction: "TestFoo",
			TestCommand:  "go test ./pkg -run TestFoo",
			Paths:        "foo.go",
		})
		if err != nil {
			t.Fatalf("RenderCardV2 failed for kind %s: %v", k, err)
		}

		if !strings.Contains(card, "## RESULT.md Fill-in Template") {
			t.Errorf("kind %s missing RESULT.md Fill-in Template section:\n%s", k, card)
		}
		if !strings.Contains(card, "## Exemplar") {
			t.Errorf("kind %s missing Exemplar section:\n%s", k, card)
		}
		if !strings.Contains(card, "CHECK: pass|fail|not-run") && !strings.Contains(card, "CHECK: pass") {
			t.Errorf("kind %s missing separate typed check conclusion (CHECK: pass):\n%s", k, card)
		}

		// Verify terminal words in template and exemplar
		if !strings.Contains(card, "DONE") || !strings.Contains(card, "ABSTAIN") || !strings.Contains(card, "BLOCKED") {
			t.Errorf("kind %s does not preserve terminal words DONE/ABSTAIN/BLOCKED:\n%s", k, card)
		}

		// Verify that "Returned" or "Verified" or "Landed" are NOT terminal result words
		lines := strings.Split(card, "\n")
		for _, ln := range lines {
			trimmed := strings.TrimSpace(ln)
			if trimmed == "Returned" || trimmed == "Verified" || trimmed == "Landed" {
				t.Errorf("card contains prohibited result status word %q as line", trimmed)
			}
		}
	}
}

func TestValidateResultV2StellaBoundaries(t *testing.T) {
	// Validates versioned common envelope against Stella's architectural boundaries.
	validFixResult := `RESULT CARD-500 sha=1234567890ab mas-bandwidth/nova-tools fix: test
DONE
CHECK: pass
BRANCH: emma/test-fix
REPO: mas-bandwidth/nova-tools
PATHS: internal/pulse/cut.go
RED: go test ./internal/pulse -run TestFoo failed with assertion error
GREEN: go test ./internal/pulse -run TestFoo passed (0.05s)
## Gates
| name | result | seconds |
| --- | --- | --- |
| go test ./internal/pulse | pass | 0.5 |
`
	res, err := ValidateResultV2(validFixResult, "fix")
	if err != nil {
		t.Fatalf("ValidateResultV2 rejected valid fix result: %v", err)
	}
	if res.Status != "DONE" {
		t.Errorf("res.Status = %q, want DONE", res.Status)
	}
	if res.Check != "pass" {
		t.Errorf("res.Check = %q, want pass", res.Check)
	}

	// 1. Terminal words must strictly be DONE, ABSTAIN, BLOCKED. Reject "Returned", "Verified", "Landed".
	for _, badWord := range []string{"Returned", "Verified", "Landed", "check-failed"} {
		badResult := strings.Replace(validFixResult, "\nDONE\n", "\n"+badWord+"\n", 1)
		if _, err := ValidateResultV2(badResult, "fix"); err == nil {
			t.Errorf("ValidateResultV2 accepted illegal status word %q", badWord)
		}
	}

	// 2. Separate typed check conclusion is required: CHECK: pass|fail|not-run
	noCheckResult := strings.Replace(validFixResult, "CHECK: pass\n", "", 1)
	if _, err := ValidateResultV2(noCheckResult, "fix"); err == nil {
		t.Errorf("ValidateResultV2 accepted result with missing CHECK: line")
	}

	// 3. Reject duplicate fields
	dupFieldResult := strings.Replace(validFixResult, "CHECK: pass\n", "CHECK: pass\nCHECK: fail\n", 1)
	if _, err := ValidateResultV2(dupFieldResult, "fix"); err == nil {
		t.Errorf("ValidateResultV2 accepted result with duplicate CHECK: line")
	}

	// 4. Worker claims strictly separate from authority: worker-authored DISPOSITION does not mint friend approval
	readResultWithDisp := `RESULT CARD-501 sha=1234567890ab mas-bandwidth/nova-tools read: test
DONE
CHECK: pass
BRANCH: emma/test-read
REPO: mas-bandwidth/nova-tools
PATHS: internal/pulse/cut.go
## Head
findings: 0
floor: HIGH
repo: mas-bandwidth/nova-tools
head: abc123def456
PR100: APPROVE head=abc123def456 repo=mas-bandwidth/nova-tools
DISPOSITION who=Worker verdict=APPROVE score=10/10
`
	readRes, err := ValidateResultV2(readResultWithDisp, "read")
	if err != nil {
		t.Fatalf("ValidateResultV2 rejected valid read result: %v", err)
	}
	if readRes.IsFriendApproval {
		t.Errorf("worker-authored DISPOSITION was permitted to mint friend approval")
	}
}
