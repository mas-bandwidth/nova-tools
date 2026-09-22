package landingindex

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	sharedIndex *Index
	sharedErr   error
	sharedOnce  sync.Once
)

func getSharedIndex(t *testing.T) *Index {
	t.Helper()
	sharedOnce.Do(func() {
		root := findRepoRoot(t)
		sharedIndex, sharedErr = Build(root)
	})
	if sharedErr != nil {
		t.Fatalf("Build failed: %v", sharedErr)
	}
	return sharedIndex
}

func findRepoRoot(t *testing.T) string {
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

func TestBuildLandingIndex(t *testing.T) {
	idx := getSharedIndex(t)

	if len(idx.SpecsByID) == 0 {
		t.Errorf("expected indexed specs, got 0")
	}
	if len(idx.TestsByName) == 0 {
		t.Errorf("expected indexed tests, got 0")
	}
	if len(idx.SymbolsByName) == 0 {
		t.Errorf("expected indexed symbols, got 0")
	}

	t.Logf("Indexed: %d specs (%d doc:line keys), %d tests, %d symbols",
		len(idx.Specs), len(idx.SpecsByDocLine), len(idx.TestsByName), len(idx.SymbolsByName))
}

func TestSpecIdToParagraph(t *testing.T) {
	idx := getSharedIndex(t)

	// 1. Check lookup by doc:line
	para := idx.LookupSpec("SPEC-CI.md:549")
	if para == nil {
		t.Fatalf("LookupSpec('SPEC-CI.md:549') returned nil")
	}
	if !strings.Contains(para.DocPath, "SPEC-CI.md") {
		t.Errorf("expected doc to contain SPEC-CI.md, got %q", para.DocPath)
	}
	if !strings.Contains(para.Text, "time.Sleep") {
		t.Errorf("expected paragraph to contain time.Sleep, got %q", para.Text)
	}

	// 2. Check lookup by section name "waits"
	waitsPara := idx.LookupSpec("waits")
	if waitsPara == nil {
		t.Fatalf("LookupSpec('waits') returned nil")
	}
	if !strings.Contains(waitsPara.Text, "TestNoFixedWaitsOnTheCIPath") {
		t.Errorf("expected waits paragraph to mention TestNoFixedWaitsOnTheCIPath, got %q", waitsPara.Text)
	}
	if len(waitsPara.GuardingTests) == 0 {
		t.Errorf("expected guarding tests for waits, got none")
	}

	// 3. Check lookup by qualified rule name "SPEC-PULSE.md:rule 5"
	r5Para := idx.LookupSpec("SPEC-PULSE.md:rule 5")
	if r5Para == nil {
		t.Fatalf("LookupSpec('SPEC-PULSE.md:rule 5') returned nil")
	}
	if !strings.Contains(r5Para.Text, "RESULT") {
		t.Errorf("expected SPEC-PULSE rule 5 to mention RESULT contract, got %q", r5Para.Text)
	}

	// 4. Invariant: ambiguous bare "rule 5" across multiple specs is rejected
	if bare := idx.LookupSpec("rule 5"); bare != nil {
		t.Fatalf("expected bare 'rule 5' to be rejected as ambiguous across multi-spec repo, got doc %s: %s", bare.DocPath, bare.Text)
	}
}

func TestMultiSpecRuleScopingAndCollisionPrevention(t *testing.T) {
	dir := t.TempDir()
	docsDir := filepath.Join(dir, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	specAlpha := `# SPEC-ALPHA

## Rules

Rule 1: Alpha rule 1 - always wear safety goggles.

Rule 5: Alpha rule 5 - use isolated worktrees for all experiments.
`
	specBeta := `# SPEC-BETA

## Rules

Rule 1: Beta rule 1 - never deploy on Friday afternoon.

Rule 5: Beta rule 5 - zero local compilations on production benches.
`

	if err := os.WriteFile(filepath.Join(docsDir, "SPEC-ALPHA.md"), []byte(specAlpha), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "SPEC-BETA.md"), []byte(specBeta), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := Build(dir)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Invariant 1: Qualified lookups retrieve target spec without cross-spec overwrites
	alphaR1 := idx.LookupSpec("SPEC-ALPHA.md:rule 1")
	if alphaR1 == nil || !strings.Contains(alphaR1.Text, "Alpha rule 1 - always wear safety goggles") {
		t.Fatalf("expected Alpha rule 1, got %+v", alphaR1)
	}

	alphaR5 := idx.LookupSpec("SPEC-ALPHA.md:rule 5")
	if alphaR5 == nil || !strings.Contains(alphaR5.Text, "Alpha rule 5 - use isolated worktrees") {
		t.Fatalf("expected Alpha rule 5, got %+v", alphaR5)
	}

	betaR1 := idx.LookupSpec("SPEC-BETA.md:rule 1")
	if betaR1 == nil || !strings.Contains(betaR1.Text, "Beta rule 1 - never deploy on Friday afternoon") {
		t.Fatalf("expected Beta rule 1, got %+v", betaR1)
	}

	betaR5 := idx.LookupSpec("SPEC-BETA.md:rule 5")
	if betaR5 == nil || !strings.Contains(betaR5.Text, "Beta rule 5 - zero local compilations") {
		t.Fatalf("expected Beta rule 5, got %+v", betaR5)
	}

	// Stem-qualified lookups (SPEC-ALPHA:rule 5 and SPEC-ALPHA rule 5)
	if p := idx.LookupSpec("SPEC-ALPHA:rule 5"); p == nil || !strings.Contains(p.Text, "isolated worktrees") {
		t.Fatalf("expected stem-qualified Alpha rule 5, got %+v", p)
	}
	if p := idx.LookupSpec("SPEC-BETA rule 5"); p == nil || !strings.Contains(p.Text, "zero local compilations") {
		t.Fatalf("expected stem-qualified Beta rule 5, got %+v", p)
	}

	// Invariant 2: Ambiguous bare rule lookups are rejected (return nil)
	if p := idx.LookupSpec("rule 1"); p != nil {
		t.Fatalf("expected ambiguous bare 'rule 1' to be rejected, got doc %s: %s", p.DocPath, p.Text)
	}
	if p := idx.LookupSpec("rule 5"); p != nil {
		t.Fatalf("expected ambiguous bare 'rule 5' to be rejected, got doc %s: %s", p.DocPath, p.Text)
	}

	// Invariant 3: FormatInlinedContext retrieves the right spec for cards naming the spec
	cardAlpha := idx.FormatInlinedContext("Implement feature under SPEC-ALPHA rule 5")
	if !strings.Contains(cardAlpha, "Alpha rule 5 - use isolated worktrees") {
		t.Errorf("cardAlpha missing Alpha rule 5:\n%s", cardAlpha)
	}
	if strings.Contains(cardAlpha, "Beta rule") {
		t.Errorf("cardAlpha erroneously contains Beta spec text:\n%s", cardAlpha)
	}

	cardBeta := idx.FormatInlinedContext("Implement feature under SPEC-BETA rule 5")
	if !strings.Contains(cardBeta, "Beta rule 5 - zero local compilations") {
		t.Errorf("cardBeta missing Beta rule 5:\n%s", cardBeta)
	}
	if strings.Contains(cardBeta, "Alpha rule") {
		t.Errorf("cardBeta erroneously contains Alpha spec text:\n%s", cardBeta)
	}

	// Invariant 4: Bare ambiguous rule in card produces no conflicting inline
	cardBare := idx.FormatInlinedContext("Implement feature under rule 5")
	if strings.Contains(cardBare, "### INLINED SPEC CONTEXT") {
		t.Errorf("bare ambiguous rule should not produce inlined spec context, got:\n%s", cardBare)
	}
}

func TestTestToCoveredFiles(t *testing.T) {
	idx := getSharedIndex(t)

	tc, ok := idx.TestsByName["TestNoFixedWaitsOnTheCIPath"]
	if !ok || tc == nil {
		t.Fatalf("TestNoFixedWaitsOnTheCIPath not indexed in TestsByName")
	}

	if !strings.Contains(tc.TestFile, "ci_waits_test.go") {
		t.Errorf("expected test file ci_waits_test.go, got %q", tc.TestFile)
	}
	if !strings.Contains(tc.TestCommand, "go test ./internal/ci -run ^TestNoFixedWaitsOnTheCIPath$") {
		t.Errorf("expected standard test command, got %q", tc.TestCommand)
	}
	if len(tc.CoveredFiles) == 0 {
		t.Errorf("expected covered files for TestNoFixedWaitsOnTheCIPath, got none")
	}
}

func TestSymbolToDefinitionAndGuardingTest(t *testing.T) {
	idx := getSharedIndex(t)

	sym, ok := idx.SymbolsByName["CutValidated"]
	if !ok || sym == nil {
		t.Fatalf("CutValidated not found in SymbolsByName")
	}

	if !strings.Contains(sym.File, "validated.go") {
		t.Errorf("expected symbol file validated.go, got %q", sym.File)
	}
	if sym.Line <= 0 {
		t.Errorf("expected valid definition line, got %d", sym.Line)
	}
	if len(sym.GuardingTests) == 0 {
		t.Errorf("expected guarding tests for CutValidated, got none")
	}
}

func TestFormatInlinedContext(t *testing.T) {
	idx := getSharedIndex(t)

	issueBody := "This issue fixes the rule at SPEC-CI.md:549 (the waits checker)."
	inlined := idx.FormatInlinedContext(issueBody)
	if inlined == "" {
		t.Fatalf("expected formatted inlined context, got empty")
	}

	if !strings.Contains(inlined, "### INLINED SPEC CONTEXT") {
		t.Errorf("expected header INLINED SPEC CONTEXT, got %q", inlined)
	}
	if !strings.Contains(inlined, "TestNoFixedWaitsOnTheCIPath") {
		t.Errorf("expected inlined text to mention TestNoFixedWaitsOnTheCIPath, got %q", inlined)
	}
	if !strings.Contains(inlined, "go test ./internal/ci") {
		t.Errorf("expected inlined test command, got %q", inlined)
	}
}
