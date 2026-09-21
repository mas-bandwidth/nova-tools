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

	// 3. Check lookup by rule name "rule 5"
	r5Para := idx.LookupSpec("rule 5")
	if r5Para == nil {
		t.Fatalf("LookupSpec('rule 5') returned nil")
	}
	if !strings.Contains(r5Para.Text, "RESULT") {
		t.Errorf("expected rule 5 to mention RESULT contract, got %q", r5Para.Text)
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
