package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/landingindex"
)

// verifyInlinedSpecContext enforces all card context invariants:
// header presence, expected document/snippet/guarding test, and bounded byte/line size.
func verifyInlinedSpecContext(card, wantDoc, wantSnippet, wantTest, wantCmd string) error {
	if !strings.Contains(card, "### INLINED SPEC CONTEXT") {
		return errors.New("card missing '### INLINED SPEC CONTEXT' header")
	}
	if wantDoc != "" && !strings.Contains(card, wantDoc) {
		return fmt.Errorf("card missing expected spec document %q", wantDoc)
	}
	if wantSnippet != "" && !strings.Contains(card, wantSnippet) {
		return fmt.Errorf("card missing expected spec snippet %q", wantSnippet)
	}
	if wantTest != "" && !strings.Contains(card, wantTest) {
		return fmt.Errorf("card missing expected guarding test %q", wantTest)
	}
	if wantCmd != "" && !strings.Contains(card, wantCmd) {
		return fmt.Errorf("card missing expected guarding test command %q", wantCmd)
	}

	// Budget validation
	lines := strings.Split(card, "\n")
	inContext := false
	contextBytes := 0
	contextLines := 0
	for _, line := range lines {
		if strings.Contains(line, "### INLINED SPEC CONTEXT") {
			inContext = true
		} else if inContext && strings.HasPrefix(line, "STEP ") {
			inContext = false
		}
		if inContext {
			contextLines++
			contextBytes += len(line) + 1
		}
	}
	if contextBytes > landingindex.MaxTotalInlinedBytes {
		return fmt.Errorf("inlined spec context exceeds total byte budget: %d > %d", contextBytes, landingindex.MaxTotalInlinedBytes)
	}
	if contextLines > landingindex.MaxTotalInlinedLines {
		return fmt.Errorf("inlined spec context exceeds total line budget: %d > %d", contextLines, landingindex.MaxTotalInlinedLines)
	}
	return nil
}

// S2: Context from landing indices.
// Done When: `nova-pulse cut` on an issue naming a spec ID prints the paragraph and the guarding test into the card.
func TestCutIssueInlinesSpecParagraphAndGuardingTest(t *testing.T) {
	specs := fakePATH(t)
	title := "Fix timer flake violating SPEC-CI.md:549 (waits rule)"
	body := "The timer in ci_waits flaked under load. We need to follow the waits rule."

	fakeTool(t, specs, "gh", fakeSpec{Default: fakeRule{Stdout: issueJSON(t, title, body)}})
	gitAnswers(t, specs, "")

	dir := t.TempDir()
	tmpl := writeValidatedTemplates(t, filepath.Join(dir, "templates"), "issue", validatedIssueTemplate)
	out := filepath.Join(dir, "out")

	// Find the real repository root so landingindex builds the true repo index
	repoRoot := findRepoRootForTest(t)

	code, stdout, stderr := runValidatedCut(t, "--issue", "mas-bandwidth/nova-tools#2498",
		"--templates", tmpl, "--out", out, "--repo", repoRoot)
	if code != 0 {
		t.Fatalf("cut --issue exit = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "CUT OK cards=1 from=issue skipped=0 out="+out) {
		t.Fatalf("stdout=%q, want CUT OK", stdout)
	}

	cardBytes, err := os.ReadFile(filepath.Join(out, "card-2498.md"))
	if err != nil {
		t.Fatalf("read cut card: %v", err)
	}
	card := string(cardBytes)

	// Verify all inlined context invariants through the real assertion
	if err := verifyInlinedSpecContext(card, "SPEC-CI.md", "time.Sleep", "TestNoFixedWaitsOnTheCIPath", "go test ./internal/ci -run ^TestNoFixedWaitsOnTheCIPath$"); err != nil {
		t.Fatalf("card context verification failed: %v\ncard:\n%s", err, card)
	}

	// Invariant: Card must list the covered files
	if !strings.Contains(card, "ci_waits_test.go") && !strings.Contains(card, "ci_waits.go") {
		t.Errorf("card missing covered file list:\n%s", card)
	}

	// Invariant: Card must still retain original issue body
	if !strings.Contains(card, body) {
		t.Errorf("card missing original issue body text:\n%s", card)
	}
}

// Negative controls with teeth: verifies that verifyInlinedSpecContext fails
// when context is missing, when a wrong-card spec is attached, and when context is oversized.
func TestCutIssueInlinesSpecMutationControl(t *testing.T) {
	validCard := `RESULT card-1 sha=123456789abc
STEP 1. mkdir -p scratch && git clone -q https://example.com/mas-bandwidth/nova-tools.git . && git checkout -b b
STEP 2. TITLE[Fix something]
STEP 3. BODY[Some issue body
### INLINED SPEC CONTEXT (indices S2)
- Spec: SPEC-CI.md:549 (docs/SPEC-CI.md:549-550)
  > time.Sleep is forbidden on CI paths.
  Guarding Tests:
  - ` + "`" + `TestNoFixedWaitsOnTheCIPath` + "`" + `: ` + "`" + `go test ./internal/ci -run ^TestNoFixedWaitsOnTheCIPath$` + "`" + `
]
STEP last. Write RESULT.md with line 1 equal to this card's line 1.`

	// Positive control: valid card passes verification
	if err := verifyInlinedSpecContext(validCard, "SPEC-CI.md", "time.Sleep", "TestNoFixedWaitsOnTheCIPath", "go test ./internal/ci"); err != nil {
		t.Fatalf("valid card failed verification: %v", err)
	}

	// Negative Control 1: Missing context header must fail
	cardNoHeader := strings.Replace(validCard, "### INLINED SPEC CONTEXT (indices S2)", "### REGULAR NOTES", 1)
	if err := verifyInlinedSpecContext(cardNoHeader, "SPEC-CI.md", "time.Sleep", "TestNoFixedWaitsOnTheCIPath", "go test ./internal/ci"); err == nil {
		t.Fatalf("mutation control: card missing inlined header must fail verification")
	}

	// Negative Control 2: Wrong-card control (unrelated spec document) must fail
	cardWrongDoc := strings.ReplaceAll(validCard, "SPEC-CI.md", "SPEC-UNRELATED.md")
	if err := verifyInlinedSpecContext(cardWrongDoc, "SPEC-CI.md", "time.Sleep", "TestNoFixedWaitsOnTheCIPath", "go test ./internal/ci"); err == nil {
		t.Fatalf("wrong-card control: card with unrelated spec document must fail verification")
	}

	// Negative Control 3: Oversized-context control must fail
	oversizedBody := "### INLINED SPEC CONTEXT (indices S2)\n" + strings.Repeat("  > excessively large line of inlined spec context that breaches the byte budget\n", 100)
	cardOversized := strings.Replace(validCard, "### INLINED SPEC CONTEXT (indices S2)\n- Spec: SPEC-CI.md:549 (docs/SPEC-CI.md:549-550)\n  > time.Sleep is forbidden on CI paths.\n  Guarding Tests:\n  - `TestNoFixedWaitsOnTheCIPath`: `go test ./internal/ci -run ^TestNoFixedWaitsOnTheCIPath$`", oversizedBody, 1)
	if err := verifyInlinedSpecContext(cardOversized, "SPEC-CI.md", "time.Sleep", "TestNoFixedWaitsOnTheCIPath", "go test ./internal/ci"); err == nil {
		t.Fatalf("oversized-context control: card exceeding byte budget must fail verification")
	}
}

func findRepoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root with go.mod")
		}
		dir = parent
	}
}
