package pulse

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
		Symbol:        "TestBoundary",
		RedWhen:       "boundary_test.go:42: expected error, got nil",
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
		Symbol:       "TestBoundary",
		RedWhen:      "large diff test fails",
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
		Symbol:       "TestEmptyQueueDoesNotPanic",
		RedWhen:      "nil pointer on empty queue",
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
		Symbol:       "TestCut",
		RedWhen:      "preflight check fails",
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
		Kind:    "read",
		Number:  105,
		Repo:    "mas-bandwidth/nova-tools",
		Title:   "review card",
		PR:      2522,
		Head:    "0526ea67fcf4ccad48f7fe573ccfbcf3a6f39745",
		Symbol:  "ValidateResultV2",
		RedWhen: "invalid envelope accepted",
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
			Symbol:       "TestFoo",
			RedWhen:      "kind test fails",
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
		Symbol:       "TestClean",
		RedWhen:      "noisy text in title",
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
			Symbol:       "TestFoo",
			RedWhen:      "clause test fails",
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
			Symbol:       "TestFoo",
			RedWhen:      "exemplar test fails",
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
		if !strings.Contains(card, "(Format illustration only; not observed evidence or historical review provenance.)") {
			t.Errorf("kind %s missing format illustration provenance notice in exemplar section:\n%s", k, card)
		}
		if !strings.Contains(card, "SCHEMA: v2") {
			t.Errorf("kind %s missing SCHEMA: v2 in template/exemplar:\n%s", k, card)
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
SCHEMA: v2
ATTEMPT: 1
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
	if res.Schema != "v2" {
		t.Errorf("res.Schema = %q, want v2", res.Schema)
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

	// 4. Failed-check DONE is accepted as a valid wire envelope representing a Returned attempt
	failedCheckDone := strings.Replace(validFixResult, "CHECK: pass\n", "CHECK: fail\n", 1)
	resFailed, err := ValidateResultV2(failedCheckDone, "fix")
	if err != nil {
		t.Errorf("ValidateResultV2 rejected failed-check DONE (wants valid wire format for Returned): %v", err)
	}
	if resFailed.Status != "DONE" || resFailed.Check != "fail" {
		t.Errorf("expected Status=DONE, Check=fail, got %q, %q", resFailed.Status, resFailed.Check)
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
SCHEMA: v2
ATTEMPT: 1
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

	// 10. Missing or invalid SCHEMA version
	noSchema := strings.Replace(validFixResult, "SCHEMA: v2\n", "", 1)
	if _, err := ValidateResultV2(noSchema, "fix"); err == nil {
		t.Errorf("ValidateResultV2 accepted result with missing SCHEMA: line")
	}
	badSchema := strings.Replace(validFixResult, "SCHEMA: v2\n", "SCHEMA: v3\n", 1)
	if _, err := ValidateResultV2(badSchema, "fix"); err == nil {
		t.Errorf("ValidateResultV2 accepted unsupported SCHEMA: v3")
	}

	// Missing ATTEMPT line must be refused
	noAttempt := strings.Replace(validFixResult, "ATTEMPT: 1\n", "", 1)
	if _, err := ValidateResultV2(noAttempt, "fix"); err == nil {
		t.Errorf("ValidateResultV2 accepted result with missing ATTEMPT: line")
	}

	// 11. ABSTAIN and BLOCKED envelopes are validated before state branching
	abstainResult := `RESULT CARD-502 sha=1234567890ab mas-bandwidth/nova-tools fix: test
ABSTAIN cannot reproduce defect
SCHEMA: v2
ATTEMPT: 1
CHECK: not-run
REPO mas-bandwidth/nova-tools
`
	resAbstain, err := ValidateResultV2(abstainResult, "fix")
	if err != nil {
		t.Fatalf("ValidateResultV2 rejected valid ABSTAIN envelope: %v", err)
	}
	if resAbstain.Status != "ABSTAIN" {
		t.Errorf("resAbstain.Status = %q, want ABSTAIN", resAbstain.Status)
	}

	// Oversized ABSTAIN must be refused
	hugeAbstain := abstainResult + strings.Repeat("# Extra text\n", 5000)
	if _, err := ValidateResultV2(hugeAbstain, "fix"); err == nil {
		t.Errorf("ValidateResultV2 accepted oversized ABSTAIN envelope (>64KB)")
	}

	// Malformed header in ABSTAIN must be refused
	malformedAbstain := strings.Replace(abstainResult, "CHECK: not-run\n", "CHECK: not-run\nILLEGAL_FIELD: val\n", 1)
	if _, err := ValidateResultV2(malformedAbstain, "fix"); err == nil {
		t.Errorf("ValidateResultV2 accepted ABSTAIN with illegal header")
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
		Symbol:        "Verdict",
		RedWhen:       "FAIL: TestMixedMessage",
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
		Symbol:       "TestCut",
		RedWhen:      "roundtrip test fails",
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
SCHEMA: v2
ATTEMPT: 1
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
		Label:   "card-202",
		Card:    "card-202.md",
		Schema:  "v2",
		Attempt: "1",
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

	// Control 1: failed-check with DONE is classified as "returned", withholds push/landing eligibility (branch=""), and does not auto-retry
	failedResult := strings.Replace(filledResult, "CHECK: pass\n", "CHECK: fail\n", 1)
	failState, failBranch, _, _ := classifyResult(row, contractLine, failedResult)
	if failState != "returned" {
		t.Errorf("classifyResult accepted failed-check DONE as state = %q, want returned", failState)
	}
	if failBranch != "" {
		t.Errorf("classifyResult for failed-check DONE granted push eligibility branch = %q, want empty", failBranch)
	}

	// Control 2: unknown header field must return mismatch
	unknownResult := strings.Replace(filledResult, "CHECK: pass\n", "CHECK: pass\nILLEGAL_HEADER: value\n", 1)
	unkState, _, _, _ := classifyResult(row, contractLine, unknownResult)
	if unkState != "mismatch" {
		t.Errorf("classifyResult accepted unknown header field as state = %q, want mismatch", unkState)
	}
}

func TestLegacyRecutCardPreservesSemantics(t *testing.T) {
	// A legacy card generated before v2 with kind "recut" carries "recut:" on line 1,
	// but does NOT declare SCHEMA: v2 and does NOT carry a CHECK: line.
	// It must pass through classifyResult without being forced through v2 CHECK validation.
	legacyContract := "RESULT CARD-101 sha=b2c3d4e5f6a1 nova-tools recut: fix boundary handling in cut"
	legacyBody := legacyContract + `
DONE
BRANCH emma/legacy-recut-branch
REPO mas-bandwidth/nova-tools
PATHS internal/pulse/cut.go
RED: test failed
GREEN: test passed
## Evidence
Notes from prior attempt.
`
	row := CardRow{Label: "card-101", Card: "card-101.md"}
	state, branch, repo, _ := classifyResult(row, legacyContract, legacyBody)
	if state != "done" {
		t.Errorf("legacy recut card failed with state = %q, want done", state)
	}
	if branch != "emma/legacy-recut-branch" {
		t.Errorf("legacy recut card branch = %q, want emma/legacy-recut-branch", branch)
	}
	if repo != "mas-bandwidth/nova-tools" {
		t.Errorf("legacy recut card repo = %q, want mas-bandwidth/nova-tools", repo)
	}
}

func TestEnvelopeOnlyFieldExtraction(t *testing.T) {
	// Verifies that classifyResult extracts effective fields ONLY from the header envelope.
	// Quoted lines or examples in markdown evidence must remain data and never override header fields.
	contractLine := "RESULT CARD-300 sha=abcdef123456 mas-bandwidth/nova-tools fix: test extraction"
	body := contractLine + `
DONE
SCHEMA: v2
ATTEMPT: 1
CHECK: pass
BRANCH emma/real-branch
REPO mas-bandwidth/nova-tools
PATHS internal/pulse/cut.go
## Evidence
Quoted command or example from another card:
BRANCH worker/quoted-fake-branch
REPO other/quoted-fake-repo
`
	row := CardRow{
		Label:   "card-300",
		Card:    "card-300.md",
		Schema:  "v2",
		Attempt: "1",
	}
	state, branch, repo, _ := classifyResult(row, contractLine, body)
	if state != "done" {
		t.Errorf("state = %q, want done", state)
	}
	if branch != "emma/real-branch" {
		t.Errorf("branch = %q, want emma/real-branch (evidence body overrode header!)", branch)
	}
	if repo != "mas-bandwidth/nova-tools" {
		t.Errorf("repo = %q, want mas-bandwidth/nova-tools", repo)
	}
}

func TestImmutableDiffArtifactRetention(t *testing.T) {
	// Verifies that diff artifacts > 6KB are retained immutably in card directory
	// and locators with digests are recorded in card markdown, for both literal and file inputs.
	queueDir := t.TempDir()
	outDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(queueDir, "NEXT"), []byte("10\n"), 0o644); err != nil {
		t.Fatalf("failed to write NEXT: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(queueDir, "lanes", "small"), 0o755); err != nil {
		t.Fatalf("failed to create lane: %v", err)
	}

	// 1. Literal diff > 6 KB
	bigDiff := strings.Repeat("diff --git a/foo.go b/foo.go\n+some changed code line\n", 200)
	sum := sha256.Sum256([]byte(bigDiff))
	expectedDigest := "sha256:" + hex.EncodeToString(sum[:])

	in := CutKindInput{
		Kind:         "recut",
		Repo:         "mas-bandwidth/nova-tools",
		Title:        "test diff retention literal",
		V2:           true,
		Out:          outDir,
		Queue:        queueDir,
		PriorDiff:    bigDiff,
		Stdout:       io.Discard,
		Stderr:       io.Discard,
		PreflightCmd: "make preflight",
		Symbol:       "FixedLoad",
		RedWhen:      "wire buffer corrupted",
	}
	code := CutKind(in)
	if code != 0 {
		t.Fatalf("CutKind failed for literal diff: code %d", code)
	}

	cardPath := filepath.Join(outDir, "card-10.md")
	diffPath := filepath.Join(outDir, "card-10.diff")

	cardBytes, err := os.ReadFile(cardPath)
	if err != nil {
		t.Fatalf("card not written: %v", err)
	}
	retainedBytes, err := os.ReadFile(diffPath)
	if err != nil {
		t.Fatalf("diff artifact not retained: %v", err)
	}
	if string(retainedBytes) != bigDiff {
		t.Errorf("retained diff content does not match input diff")
	}
	if !strings.Contains(string(cardBytes), expectedDigest) {
		t.Errorf("card missing expected digest %s:\n%s", expectedDigest, string(cardBytes))
	}
	if !strings.Contains(string(cardBytes), diffPath) {
		t.Errorf("card missing retained diff path %s:\n%s", diffPath, string(cardBytes))
	}
	if !strings.Contains(string(cardBytes), fmt.Sprintf("omitted %d bytes", len(strings.TrimSpace(bigDiff))-DefaultDiffCap)) {
		t.Errorf("card missing omitted bytes notice:\n%s", string(cardBytes))
	}

	// 2. File diff > 6 KB
	diffFile := filepath.Join(t.TempDir(), "input.patch")
	if err := os.WriteFile(diffFile, []byte(bigDiff), 0o644); err != nil {
		t.Fatalf("failed to write diffFile: %v", err)
	}
	gitDir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = gitDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	in2 := CutKindInput{
		Kind:         "recut",
		Repo:         "mas-bandwidth/nova-tools",
		Title:        "test diff retention file",
		V2:           true,
		Out:          outDir,
		Queue:        queueDir,
		DiffFile:     diffFile,
		Dir:          gitDir,
		Stdout:       io.Discard,
		Stderr:       io.Discard,
		PreflightCmd: "make preflight",
		Symbol:       "FixedLoad",
		RedWhen:      "wire buffer corrupted",
	}
	code = CutKind(in2)
	if code != 0 {
		t.Fatalf("CutKind failed for file diff: code %d", code)
	}
	card11Path := filepath.Join(outDir, "card-11.md")
	diff11Path := filepath.Join(outDir, "card-11.diff")

	card11Bytes, err := os.ReadFile(card11Path)
	if err != nil {
		t.Fatalf("card-11 not written: %v", err)
	}
	retained11Bytes, err := os.ReadFile(diff11Path)
	if err != nil {
		t.Fatalf("diff artifact 11 not retained: %v", err)
	}
	if string(retained11Bytes) != bigDiff {
		t.Errorf("retained diff 11 content does not match input diff")
	}
	if !strings.Contains(string(card11Bytes), diff11Path) {
		t.Errorf("card-11 missing retained diff path %s:\n%s", diff11Path, string(card11Bytes))
	}
	if !strings.Contains(string(card11Bytes), expectedDigest) {
		t.Errorf("card-11 missing expected digest %s:\n%s", expectedDigest, string(card11Bytes))
	}
	if !strings.Contains(string(card11Bytes), fmt.Sprintf("omitted %d bytes", len(strings.TrimSpace(bigDiff))-DefaultDiffCap)) {
		t.Errorf("card-11 missing omitted bytes notice:\n%s", string(card11Bytes))
	}

	// 3. Conflicting diff inputs: both --diff-file and --prior-diff
	var conflictErr bytes.Buffer
	inConflict := CutKindInput{
		Kind:         "recut",
		Repo:         "mas-bandwidth/nova-tools",
		Title:        "test conflicting diffs",
		V2:           true,
		Out:          outDir,
		Queue:        queueDir,
		DiffFile:     diffFile,
		PriorDiff:    bigDiff,
		Dir:          gitDir,
		Stdout:       io.Discard,
		Stderr:       &conflictErr,
		PreflightCmd: "make preflight",
		Symbol:       "FixedLoad",
		RedWhen:      "wire buffer corrupted",
	}
	if code := CutKind(inConflict); code != 2 {
		t.Errorf("CutKind with conflicting diff inputs returned code %d, want 2", code)
	}
	if !strings.Contains(conflictErr.String(), "conflicting diff inputs") {
		t.Errorf("CutKind did not name conflicting diff inputs: %s", conflictErr.String())
	}

	// 4. Unwritable artifact destination: refusal when retention fails
	unwritableOut := filepath.Join(t.TempDir(), "readonly-out")
	if err := os.MkdirAll(unwritableOut, 0o555); err != nil {
		t.Fatalf("failed to create unwritable directory: %v", err)
	}
	// On macOS/Unix ensure directory is read-only
	if err := os.Chmod(unwritableOut, 0o555); err != nil {
		t.Fatalf("failed to chmod directory: %v", err)
	}
	var unwriteErr bytes.Buffer
	inUnwritable := CutKindInput{
		Kind:         "recut",
		Repo:         "mas-bandwidth/nova-tools",
		Title:        "test unwritable destination",
		V2:           true,
		Out:          unwritableOut,
		Queue:        queueDir,
		PriorDiff:    bigDiff,
		Stdout:       io.Discard,
		Stderr:       &unwriteErr,
		PreflightCmd: "make preflight",
		Symbol:       "FixedLoad",
		RedWhen:      "wire buffer corrupted",
	}
	if code := CutKind(inUnwritable); code != 2 {
		t.Errorf("CutKind to unwritable destination returned code %d, want 2", code)
	}
	if !strings.Contains(unwriteErr.String(), "pass a writable --out directory") {
		t.Errorf("CutKind did not name writable directory refusal: %s", unwriteErr.String())
	}
	// Ensure no card claiming a retained artifact was written
	if files, err := os.ReadDir(unwritableOut); err == nil && len(files) > 0 {
		t.Errorf("CutKind wrote files to unwritable directory: %v", files)
	}
}

func TestCardTemplateV2A10SymbolAndRedWhen(t *testing.T) {
	// A10: Every card must carry SYMBOL: and RED-WHEN:.
	// Cutter lint refuses a card without them.
	card, err := RenderCardV2(CardV2Input{
		Kind:         "fix",
		Number:       150,
		Repo:         "mas-bandwidth/nova-tools",
		Title:        "test symbol and red when",
		Branch:       "emma/test-a10",
		Base:         "dev",
		Location:     "internal/pulse/cut.go:1",
		TestPackage:  "./internal/pulse",
		TestFunction: "TestCut",
		TestCommand:  "go test ./internal/pulse -run TestCut",
		Paths:        "internal/pulse/cut.go",
		PreflightCmd: "make preflight",
		Symbol:       "FixedLoad",
		RedWhen:      "wire buffer corrupted or uninitialized",
	})
	if err != nil {
		t.Fatalf("RenderCardV2 failed: %v", err)
	}

	if !strings.Contains(card, "SYMBOL: FixedLoad") {
		t.Errorf("card missing expected SYMBOL: line:\n%s", card)
	}
	if !strings.Contains(card, "RED-WHEN: wire buffer corrupted or uninitialized") {
		t.Errorf("card missing expected RED-WHEN: line:\n%s", card)
	}
}

func TestValidateCardV2CutterLint(t *testing.T) {
	validCard := `RESULT CARD-151 sha=123456789abc tools fix: test card
You are a worker. Turn budget: 20 turns. Deadline is the machinery's.

## Target & Scope
REPO: mas-bandwidth/nova-tools
SCHEMA: v2
ATTEMPT: 1
SYMBOL: TableFixedReport
RED-WHEN: test asserts self-written buffer without calling runtime
`
	if err := ValidateCardV2(validCard); err != nil {
		t.Errorf("ValidateCardV2 rejected valid card: %v", err)
	}

	// Missing SYMBOL
	missingSymbol := strings.Replace(validCard, "SYMBOL: TableFixedReport\n", "", 1)
	if err := ValidateCardV2(missingSymbol); err == nil || !strings.Contains(err.Error(), "missing required SYMBOL:") {
		t.Errorf("ValidateCardV2 accepted card without SYMBOL: err=%v", err)
	}

	// Empty SYMBOL
	emptySymbol := strings.Replace(validCard, "SYMBOL: TableFixedReport\n", "SYMBOL: \n", 1)
	if err := ValidateCardV2(emptySymbol); err == nil || !strings.Contains(err.Error(), "missing required SYMBOL:") {
		t.Errorf("ValidateCardV2 accepted card with empty SYMBOL: err=%v", err)
	}

	// Missing RED-WHEN
	missingRedWhen := strings.Replace(validCard, "RED-WHEN: test asserts self-written buffer without calling runtime\n", "", 1)
	if err := ValidateCardV2(missingRedWhen); err == nil || !strings.Contains(err.Error(), "missing required RED-WHEN:") {
		t.Errorf("ValidateCardV2 accepted card without RED-WHEN: err=%v", err)
	}

	// Empty RED-WHEN
	emptyRedWhen := strings.Replace(validCard, "RED-WHEN: test asserts self-written buffer without calling runtime\n", "RED-WHEN: \n", 1)
	if err := ValidateCardV2(emptyRedWhen); err == nil || !strings.Contains(err.Error(), "missing required RED-WHEN:") {
		t.Errorf("ValidateCardV2 accepted card with empty RED-WHEN: err=%v", err)
	}

	// Missing SCHEMA: v2
	missingSchema := strings.Replace(validCard, "SCHEMA: v2\n", "", 1)
	if err := ValidateCardV2(missingSchema); err == nil || !strings.Contains(err.Error(), "missing required SCHEMA: v2") {
		t.Errorf("ValidateCardV2 accepted card without SCHEMA: v2: err=%v", err)
	}

	// Forbidden operative git add -A (A4)
	gitAddACard := validCard + "STEP 3. git add -A && git commit\n"
	if err := ValidateCardV2(gitAddACard); err == nil || !strings.Contains(err.Error(), "forbidden operative broad staging") {
		t.Errorf("ValidateCardV2 accepted card containing operative 'git add -A': err=%v", err)
	}

	gitAddAllCard := validCard + "STEP 3. git add --all && git commit\n"
	if err := ValidateCardV2(gitAddAllCard); err == nil || !strings.Contains(err.Error(), "forbidden operative broad staging") {
		t.Errorf("ValidateCardV2 accepted card containing operative 'git add --all': err=%v", err)
	}

	// Permitted quoted evidence: RED-WHEN naming git add -A, HOLD, Inlined Evidence, backtick quotes
	quotedEvidenceCard := validCard + "RED-WHEN: cutter lint fails on 'git add -A'\nHOLD: remove git add -A\nTask: remove `git add -A`\n"
	if err := ValidateCardV2(quotedEvidenceCard); err != nil {
		t.Errorf("ValidateCardV2 rejected card quoting 'git add -A' as evidence: %v", err)
	}
}

func TestCardTemplateV2A4CommitRuleAndGitAddRefusal(t *testing.T) {
	// A4: commit rule stages PATHS only, never git add -A; notes live outside repo/.
	// DONE WHEN the cutter's lint refuses a card containing git add -A.
	card, err := RenderCardV2(CardV2Input{
		Kind:         "fix",
		Number:       153,
		Repo:         "mas-bandwidth/nova-tools",
		Title:        "test commit rule",
		Branch:       "emma/commit-rule",
		Base:         "dev",
		Location:     "internal/pulse/cut.go:1",
		TestPackage:  "./internal/pulse",
		TestFunction: "TestFoo",
		TestCommand:  "go test ./internal/pulse -run TestFoo",
		Paths:        "internal/pulse/cut.go",
		Symbol:       "ValidateCardV2",
		RedWhen:      "staging entire working tree",
	})
	if err != nil {
		t.Fatalf("RenderCardV2 failed: %v", err)
	}
	if !strings.Contains(card, "Commit rule: Stage declared PATHS only; notes and scratch live outside repo/.") {
		t.Errorf("RenderCardV2 missing A4 commit rule:\n%s", card)
	}

	// Legitimate repair card quoting 'git add -A' as evidence must be accepted (Stella A4)
	repairCard, err := RenderCardV2(CardV2Input{
		Kind:         "fix",
		Number:       154,
		Repo:         "mas-bandwidth/nova-tools",
		Title:        "remove git add -A from deploy script",
		Branch:       "emma/fix-deploy-staging",
		Base:         "dev",
		Location:     "internal/pulse/cut.go:1",
		TestPackage:  "./internal/pulse",
		TestFunction: "TestFoo",
		TestCommand:  "go test ./internal/pulse -run TestFoo",
		Paths:        "internal/pulse/cut.go",
		Symbol:       "ValidateCardV2",
		RedWhen:      "cutter lint fails to refuse card containing 'git add -A'",
		HoldLine:     "HOLD: remove git add -A from Makefile",
		ReviewerLine: "Reviewer verdict: found broad staging git add -A in deploy script",
		PriorDiff:    "--- a/deploy.sh\n+++ b/deploy.sh\n-git add -A\n+git add $PATHS",
		Body:         "Task: remove `git add -A` and `git add --all` from deploy script. Quote: > do not run git add -A",
	})
	if err != nil {
		t.Fatalf("RenderCardV2 rejected legitimate repair card quoting 'git add -A' as evidence: %v", err)
	}
	if !strings.Contains(repairCard, "remove `git add -A`") {
		t.Errorf("RenderCardV2 dropped task text:\n%s", repairCard)
	}

	// Card with operative broad staging in COMMAND must be refused
	_, err = RenderCardV2(CardV2Input{
		Kind:         "fix",
		Number:       155,
		Repo:         "mas-bandwidth/nova-tools",
		Title:        "test broad staging command",
		Branch:       "emma/commit-rule-bad-cmd",
		Base:         "dev",
		Location:     "internal/pulse/cut.go:1",
		TestPackage:  "./internal/pulse",
		TestFunction: "TestFoo",
		TestCommand:  "git add -A && go test ./...",
		Paths:        "internal/pulse/cut.go",
		Symbol:       "ValidateCardV2",
		RedWhen:      "staging entire working tree",
	})
	if err == nil || !strings.Contains(err.Error(), "COMMAND contains forbidden operative broad staging") {
		t.Errorf("RenderCardV2 accepted card with git add -A in COMMAND: err=%v", err)
	}

	// Card containing operative broad staging in body must be refused
	_, err = RenderCardV2(CardV2Input{
		Kind:         "fix",
		Number:       156,
		Repo:         "mas-bandwidth/nova-tools",
		Title:        "test forbidden git add",
		Branch:       "emma/commit-rule-bad",
		Base:         "dev",
		Location:     "internal/pulse/cut.go:1",
		TestPackage:  "./internal/pulse",
		TestFunction: "TestFoo",
		TestCommand:  "go test ./internal/pulse -run TestFoo",
		Paths:        "internal/pulse/cut.go",
		Symbol:       "ValidateCardV2",
		RedWhen:      "git add -A accepted",
		Body:         "STEP 3. git add -A && git commit -m 'oops'",
	})
	if err == nil || !strings.Contains(err.Error(), "forbidden operative broad staging") {
		t.Errorf("RenderCardV2 accepted card with git add -A in body: err=%v", err)
	}
}

func TestRenderCardV2RefusesMissingSymbolOrRedWhen(t *testing.T) {
	baseInput := CardV2Input{
		Kind:         "fix",
		Number:       152,
		Repo:         "mas-bandwidth/nova-tools",
		Title:        "test refusal",
		Branch:       "emma/test-refusal",
		Base:         "dev",
		Location:     "internal/pulse/cut.go:1",
		TestPackage:  "./internal/pulse",
		TestFunction: "TestCut",
		TestCommand:  "go test ./internal/pulse -run TestCut",
		Paths:        "internal/pulse/cut.go",
		PreflightCmd: "make preflight",
	}

	// Missing Symbol
	noSym := baseInput
	noSym.RedWhen = "some failure"
	if _, err := RenderCardV2(noSym); err == nil || !strings.Contains(err.Error(), "requires Symbol") {
		t.Errorf("RenderCardV2 accepted input without Symbol: err=%v", err)
	}

	// Empty Symbol
	emptySym := baseInput
	emptySym.Symbol = "   "
	emptySym.RedWhen = "some failure"
	if _, err := RenderCardV2(emptySym); err == nil || !strings.Contains(err.Error(), "requires Symbol") {
		t.Errorf("RenderCardV2 accepted input with whitespace Symbol: err=%v", err)
	}

	// Missing RedWhen
	noRed := baseInput
	noRed.Symbol = "FixedLoad"
	if _, err := RenderCardV2(noRed); err == nil || !strings.Contains(err.Error(), "requires RedWhen") {
		t.Errorf("RenderCardV2 accepted input without RedWhen: err=%v", err)
	}

	// Empty RedWhen
	emptyRed := baseInput
	emptyRed.Symbol = "FixedLoad"
	emptyRed.RedWhen = "   "
	if _, err := RenderCardV2(emptyRed); err == nil || !strings.Contains(err.Error(), "requires RedWhen") {
		t.Errorf("RenderCardV2 accepted input with whitespace RedWhen: err=%v", err)
	}
}
