package pulse

import (
	"fmt"
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

	// Test diff capping at 6 KB with explicit artifact reference
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
		DiffArtifact: "artifacts/diff-102.patch",
	})
	if err != nil {
		t.Fatalf("RenderCardV2 failed with large diff: %v", err)
	}
	if !strings.Contains(cappedCard, "[inlined diff capped at 6144 bytes; omitted") {
		t.Errorf("large diff was not capped with omission notice:\n%s", cappedCard)
	}
	if !strings.Contains(cappedCard, "full diff retained at artifacts/diff-102.patch") {
		t.Errorf("capped diff did not reference retained artifact path:\n%s", cappedCard)
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
		"Read scope: Contextual reads are permitted for callers, callees, contracts, fixtures, build inputs, and reverse dependents needed to verify the task.",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("card missing expected named place / command element %q:\n%s", want, card)
		}
	}

	// Read scope line must appear exactly once, not duplicated
	readScopeCount := strings.Count(card, "Read scope:")
	if readScopeCount != 1 {
		t.Errorf("Read scope line appeared %d times, want exactly 1", readScopeCount)
	}

	// Must NOT contain the contradictory "Do not read outside PATHS"
	if strings.Contains(card, "Do not read outside PATHS") {
		t.Errorf("card contains contradictory 'Do not read outside PATHS' restriction")
	}
}

func TestCardTemplateV2A3PreflightLine(t *testing.T) {
	// A3: Preflight line in card. Coherent with read/report rules.
	fixCard, err := RenderCardV2(CardV2Input{
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

	if !strings.Contains(fixCard, "make preflight") {
		t.Errorf("fix card does not contain preflight command:\n%s", fixCard)
	}
	if !strings.Contains(fixCard, "Before writing RESULT.md, run the class-rule preflight on the current tip") {
		t.Errorf("fix card does not contain preflight instruction before RESULT.md:\n%s", fixCard)
	}

	// Read card: must NOT demand make preflight (read-only)
	readCard, err := RenderCardV2(CardV2Input{
		Kind:   "read",
		Number: 105,
		Repo:   "mas-bandwidth/nova-tools",
		Title:  "review card",
		PR:     2522,
		Head:   "0526ea67fcf4ccad48f7fe573ccfbcf3a6f39745",
	})
	if err != nil {
		t.Fatalf("RenderCardV2 failed for read card: %v", err)
	}
	if strings.Contains(readCard, "run the class-rule preflight") || strings.Contains(readCard, "make preflight") {
		t.Errorf("read card inappropriately ordered toolchain preflight execution:\n%s", readCard)
	}
	if !strings.Contains(readCard, "verify that every quoted rule has file:line") {
		t.Errorf("read card missing rule-verification preflight instruction:\n%s", readCard)
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
	// A7: RESULT.md fill-in template with one format illustration per kind.
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

		// Verify space-delimited wire shape in template: BRANCH <branch>, REPO <repo>
		if !strings.Contains(card, "BRANCH <working branch>") {
			t.Errorf("kind %s does not use wire-compatible BRANCH <working branch>:\n%s", k, card)
		}
		if !strings.Contains(card, "REPO <owner>/<name>") {
			t.Errorf("kind %s does not use wire-compatible REPO <owner>/<name>:\n%s", k, card)
		}

		// Line 1 in skeleton must not prepend duplicate RESULT
		if strings.Contains(card, "RESULT <line 1") {
			t.Errorf("kind %s prepends redundant RESULT to line 1 verbatim instruction:\n%s", k, card)
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
BRANCH emma/test-fix
REPO mas-bandwidth/nova-tools
PATHS internal/pulse/cut.go
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

	// 4. Failed-check DONE must be refused
	failedCheckDone := strings.Replace(validFixResult, "CHECK: pass\n", "CHECK: fail\n", 1)
	if _, err := ValidateResultV2(failedCheckDone, "fix"); err == nil {
		t.Errorf("ValidateResultV2 accepted failed-check DONE")
	}

	// 5. Unknown fields before markdown header must be refused
	unknownFieldResult := strings.Replace(validFixResult, "CHECK: pass\n", "CHECK: pass\nUNKNOWN_HEADER: illegal\n", 1)
	if _, err := ValidateResultV2(unknownFieldResult, "fix"); err == nil {
		t.Errorf("ValidateResultV2 accepted unknown header field")
	}

	// 6. Colon-delimited headers must also be accepted
	colonResult := strings.Replace(validFixResult, "BRANCH emma/test-fix\n", "BRANCH: emma/test-fix\n", 1)
	colonResult = strings.Replace(colonResult, "REPO mas-bandwidth/nova-tools\n", "REPO: mas-bandwidth/nova-tools\n", 1)
	if _, err := ValidateResultV2(colonResult, "fix"); err != nil {
		t.Errorf("ValidateResultV2 rejected colon-delimited headers: %v", err)
	}

	// 7. Malformed lines before markdown header must be refused
	malformedResult := strings.Replace(validFixResult, "CHECK: pass\n", "CHECK: pass\nthis is not a header\n", 1)
	if _, err := ValidateResultV2(malformedResult, "fix"); err == nil {
		t.Errorf("ValidateResultV2 accepted malformed header line")
	}

	// 8. Envelope size upper bound (MaxResultEnvelopeSize = 64 KB)
	hugeResult := validFixResult + strings.Repeat("# Extra text\n", 5000)
	if _, err := ValidateResultV2(hugeResult, "fix"); err == nil {
		t.Errorf("ValidateResultV2 accepted oversized RESULT.md (>64KB)")
	}

	// 9. Worker claims strictly separate from authority: worker-authored DISPOSITION does not mint friend approval
	readResultWithDisp := `RESULT CARD-501 sha=1234567890ab mas-bandwidth/nova-tools read: test
DONE
CHECK: pass
BRANCH emma/test-read
REPO mas-bandwidth/nova-tools
PATHS internal/pulse/cut.go
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

func TestRenderCardIdentityRetention(t *testing.T) {
	// Verifies that CardV2Input retains Head, PR, Issue, BaseSHA, Remains, HoldLine, Body
	card, err := RenderCardV2(CardV2Input{
		Kind:          "recut",
		Number:        201,
		Repo:          "mas-bandwidth/nova-tools",
		Title:         "recut with full identity",
		Branch:        "worker/recut-201",
		Base:          "dev",
		BaseSHA:       "60df906de548a6bca0579261cc5b618e70cda7ee",
		Head:          "b2cc4f13a459e5dded17c27663a324ca4895985e",
		PR:            2512,
		Issue:         2454,
		Location:      "internal/merge/verdict.go:10",
		Paths:         "internal/merge/verdict.go",
		Remains:       "internal/merge/verdict.go:120",
		HoldLine:      "DISPOSITION who=stella verdict=HOLD",
		HoldFile:      "scratch/hold.txt",
		Applied:       "patch applied cleanly",
		Body:          "This is the required task body text that must never be dropped.",
		ReviewerLine:  "DISPOSITION who=stella verdict=HOLD",
		FailingOutput: "FAIL: TestMixedMessage",
	})
	if err != nil {
		t.Fatalf("RenderCardV2 failed: %v", err)
	}

	for _, check := range []string{
		"PR: 2512",
		"ISSUE: 2454",
		"HEAD: b2cc4f13a459e5dded17c27663a324ca4895985e",
		"BASE: dev",
		"BASE_SHA: 60df906de548a6bca0579261cc5b618e70cda7ee",
		"REMAINS: internal/merge/verdict.go:120",
		"HOLD: DISPOSITION who=stella verdict=HOLD",
		"HOLD_FILE: scratch/hold.txt",
		"APPLIED: patch applied cleanly",
		"## Task\nThis is the required task body text that must never be dropped.",
	} {
		if !strings.Contains(card, check) {
			t.Errorf("card missing expected retained identity / context %q:\n%s", check, card)
		}
	}
}

func TestHarvestV2RoundTrip(t *testing.T) {
	// Verifies end-to-end round trip: v2 card cut -> filled RESULT.md -> harvest classifyResult
	card, err := RenderCardV2(CardV2Input{
		Kind:         "fix",
		Number:       202,
		Repo:         "mas-bandwidth/nova-tools",
		Title:        "roundtrip test fix",
		Branch:       "emma/fix-roundtrip",
		Base:         "dev",
		Location:     "internal/pulse/cut.go:1",
		TestPackage:  "./internal/pulse",
		TestFunction: "TestCut",
		TestCommand:  "go test ./internal/pulse -run TestCut",
		Paths:        "internal/pulse/cut.go",
	})
	if err != nil {
		t.Fatalf("RenderCardV2 failed: %v", err)
	}

	// Extract contract line 1
	lines := strings.Split(card, "\n")
	contractLine := lines[0]

	// Simulate worker writing valid v2 RESULT.md matching template
	filledResult := fmt.Sprintf(`%s
DONE
CHECK: pass
BRANCH emma/fix-roundtrip
REPO mas-bandwidth/nova-tools
PATHS internal/pulse/cut.go
RED: test failed
GREEN: test passed
## Gates
| name | result | seconds |
| --- | --- | --- |
| test | pass | 0.1 |
`, contractLine)

	row := CardRow{
		Label: "card-202",
		Card:  "card-202.md",
	}

	state, branch, repo, _ := classifyResult(row, contractLine, filledResult)
	if state != "done" {
		t.Errorf("classifyResult state = %q, want done", state)
	}
	if branch != "emma/fix-roundtrip" {
		t.Errorf("classifyResult branch = %q, want emma/fix-roundtrip", branch)
	}
	if repo != "mas-bandwidth/nova-tools" {
		t.Errorf("classifyResult repo = %q, want mas-bandwidth/nova-tools", repo)
	}

	// Negative control 1: failed-check with DONE must return mismatch
	failedResult := strings.Replace(filledResult, "CHECK: pass\n", "CHECK: fail\n", 1)
	failState, _, _, _ := classifyResult(row, contractLine, failedResult)
	if failState != "mismatch" {
		t.Errorf("classifyResult accepted failed-check DONE as state = %q, want mismatch", failState)
	}

	// Negative control 2: unknown header field must return mismatch
	unknownResult := strings.Replace(filledResult, "CHECK: pass\n", "CHECK: pass\nILLEGAL_HEADER: value\n", 1)
	unkState, _, _, _ := classifyResult(row, contractLine, unknownResult)
	if unkState != "mismatch" {
		t.Errorf("classifyResult accepted unknown header field as state = %q, want mismatch", unkState)
	}
}
