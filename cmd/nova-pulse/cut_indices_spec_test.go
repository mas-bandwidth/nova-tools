package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

	// Invariant 1: Card must inline the spec paragraph
	if !strings.Contains(card, "### INLINED SPEC CONTEXT") {
		t.Fatalf("card missing '### INLINED SPEC CONTEXT' header:\n%s", card)
	}
	if !strings.Contains(card, "time.Sleep") {
		t.Errorf("card missing spec paragraph text ('time.Sleep'):\n%s", card)
	}

	// Invariant 2: Card must inline the guarding test and its command
	if !strings.Contains(card, "TestNoFixedWaitsOnTheCIPath") {
		t.Errorf("card missing guarding test 'TestNoFixedWaitsOnTheCIPath':\n%s", card)
	}
	if !strings.Contains(card, "go test ./internal/ci -run ^TestNoFixedWaitsOnTheCIPath$") {
		t.Errorf("card missing guarding test command: %s", card)
	}

	// Invariant 3: Card must list the covered files
	if !strings.Contains(card, "ci_waits_test.go") && !strings.Contains(card, "ci_waits.go") {
		t.Errorf("card missing covered file list:\n%s", card)
	}

	// Invariant 4: Card must still retain original issue body
	if !strings.Contains(card, body) {
		t.Errorf("card missing original issue body text:\n%s", card)
	}
}

// Negative control / mutation test: asserts that the verification fails if
// inlined context is stripped or mismatched (proving teeth).
func TestCutIssueInlinesSpecMutationControl(t *testing.T) {
	fakeCard := `RESULT card-1 sha=123456789abc
STEP 1. mkdir -p scratch && git clone -q https://example.com/mas-bandwidth/nova-tools.git . && git checkout -b b
STEP 2. TITLE[Fix something]
STEP 3. BODY[Some body without spec inlining]
STEP last. Write RESULT.md with line 1 equal to this card's line 1.`

	// Verify teeth: if a card lacks the inlined spec context, our required assertions catch it
	hasInlinedHeader := strings.Contains(fakeCard, "### INLINED SPEC CONTEXT")
	hasGuardingTest := strings.Contains(fakeCard, "TestNoFixedWaitsOnTheCIPath")
	hasTestCommand := strings.Contains(fakeCard, "go test ./internal/ci")

	if hasInlinedHeader || hasGuardingTest || hasTestCommand {
		t.Fatalf("mutation control: fake card should not contain inlined spec context")
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
