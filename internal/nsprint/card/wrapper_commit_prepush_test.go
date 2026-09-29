package card_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// TestRunPrepushTestExecution verifies RunPrepushTest parsing of test.json,
// handling of pass, fail, build-fail, and timeout.
func TestRunPrepushTestExecution(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("empty-pkgs-passes", func(t *testing.T) {
		t.Parallel()
		res, err := card.RunPrepushTest(ctx, t.TempDir(), nil)
		if err != nil || !res.Passed {
			t.Fatalf("empty pkgs: res=%+v, err=%v", res, err)
		}
		if res.Receipt != "make test PKGS= GOTEST_COUNT_FLAG= pass" {
			t.Errorf("receipt = %q", res.Receipt)
		}
	})

	t.Run("failing-tests-json", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		mk := "test:\n" +
			"\t@echo '{\"Action\":\"run\",\"Package\":\"pkg/a\",\"Test\":\"TestAlpha\"}' > $${RUNNER_TEMP}/test.json\n" +
			"\t@echo '{\"Action\":\"fail\",\"Package\":\"pkg/a\",\"Test\":\"TestAlpha\"}' >> $${RUNNER_TEMP}/test.json\n" +
			"\t@echo '{\"Action\":\"run\",\"Package\":\"pkg/b\",\"Test\":\"TestBeta\"}' >> $${RUNNER_TEMP}/test.json\n" +
			"\t@echo '{\"Action\":\"fail\",\"Package\":\"pkg/b\",\"Test\":\"TestBeta\"}' >> $${RUNNER_TEMP}/test.json\n" +
			"\t@exit 1\n"
		if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(mk), 0o644); err != nil {
			t.Fatal(err)
		}

		res, err := card.RunPrepushTest(ctx, dir, []string{"./pkg/a", "./pkg/b"})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if res.Passed {
			t.Fatalf("expected failure, got passed")
		}
		wantFails := []string{"TestAlpha", "TestBeta"}
		if !reflect.DeepEqual(res.FailingTests, wantFails) {
			t.Fatalf("got failing %v, want %v", res.FailingTests, wantFails)
		}
	})

	t.Run("passing-tests-json", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		mk := "test:\n" +
			"\t@echo '{\"Action\":\"run\",\"Package\":\"pkg/a\",\"Test\":\"TestGood\"}' > $${RUNNER_TEMP}/test.json\n" +
			"\t@echo '{\"Action\":\"pass\",\"Package\":\"pkg/a\",\"Test\":\"TestGood\"}' >> $${RUNNER_TEMP}/test.json\n" +
			"\t@exit 0\n"
		if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(mk), 0o644); err != nil {
			t.Fatal(err)
		}

		res, err := card.RunPrepushTest(ctx, dir, []string{"./pkg/a"})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !res.Passed {
			t.Fatalf("expected pass, got failed")
		}
		wantReceipt := "make test PKGS=./pkg/a GOTEST_COUNT_FLAG= pass"
		if res.Receipt != wantReceipt {
			t.Fatalf("receipt = %q, want %q", res.Receipt, wantReceipt)
		}
	})

	t.Run("package-build-fail-no-tests", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		mk := "test:\n" +
			"\t@echo '{\"Action\":\"build-fail\",\"Package\":\"pkg/broken\"}' > $${RUNNER_TEMP}/test.json\n" +
			"\t@exit 1\n"
		if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(mk), 0o644); err != nil {
			t.Fatal(err)
		}

		res, err := card.RunPrepushTest(ctx, dir, []string{"./pkg/broken"})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if res.Passed {
			t.Fatalf("expected failure, got passed")
		}
		wantFails := []string{"pkg/broken"}
		if !reflect.DeepEqual(res.FailingTests, wantFails) {
			t.Fatalf("got failing %v, want %v", res.FailingTests, wantFails)
		}
	})
}

// TestDerivePrepushPackagesCoversPaths verifies package derivation across
// exact Go files, globs, non-Go files, 1-level-up importers, and class tests.
func TestDerivePrepushPackagesCoversPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("docs-only-carries-class-tests", func(t *testing.T) {
		t.Parallel()
		pkgs, err := card.DerivePrepushPackages(ctx, t.TempDir(), "docs/SEED-CORE.md docs/** README.md")
		if err != nil {
			t.Fatalf("derive: %v", err)
		}
		want := []string{"./internal/ci", "./internal/docs"}
		if !reflect.DeepEqual(pkgs, want) {
			t.Fatalf("got %v, want %v", pkgs, want)
		}
	})

	t.Run("empty-paths-carries-class-tests", func(t *testing.T) {
		t.Parallel()
		pkgs, err := card.DerivePrepushPackages(ctx, t.TempDir(), "")
		if err != nil {
			t.Fatalf("derive: %v", err)
		}
		want := []string{"./internal/ci", "./internal/docs"}
		if !reflect.DeepEqual(pkgs, want) {
			t.Fatalf("got %v, want %v", pkgs, want)
		}
	})

	t.Run("repo-tree-derivation-and-importers", func(t *testing.T) {
		t.Parallel()
		// Get current working directory (the nova-tools repository)
		repoRoot, err := filepath.Abs("../../..")
		if err != nil {
			t.Fatal(err)
		}

		// Touch card package in nova-tools:
		// Touched: ./internal/nsprint/card
		// Class tests: ./internal/ci, ./internal/docs
		// 1-level-up importers of internal/nsprint/card include ./internal/nsprint/reconcile
		pkgs, err := card.DerivePrepushPackages(ctx, repoRoot, "internal/nsprint/card/copy_ledger.go")
		if err != nil {
			t.Fatalf("derive: %v", err)
		}

		mustContain := []string{"./internal/ci", "./internal/docs", "./internal/nsprint/card", "./internal/nsprint/reconcile"}
		pkgMap := make(map[string]bool)
		for _, p := range pkgs {
			pkgMap[p] = true
		}
		for _, want := range mustContain {
			if !pkgMap[want] {
				t.Errorf("derived packages %v missing %s", pkgs, want)
			}
		}

		// Verify sorted
		if !sort.StringsAreSorted(pkgs) {
			t.Errorf("packages %v are not sorted", pkgs)
		}
	})

	t.Run("globs-and-directories", func(t *testing.T) {
		t.Parallel()
		repoRoot, err := filepath.Abs("../../..")
		if err != nil {
			t.Fatal(err)
		}

		pkgs, err := card.DerivePrepushPackages(ctx, repoRoot, "internal/nsprint/card/*.go internal/docs/**")
		if err != nil {
			t.Fatalf("derive: %v", err)
		}
		pkgMap := make(map[string]bool)
		for _, p := range pkgs {
			pkgMap[p] = true
		}
		if !pkgMap["./internal/nsprint/card"] {
			t.Errorf("packages %v missing ./internal/nsprint/card", pkgs)
		}
		if !pkgMap["./internal/ci"] {
			t.Errorf("packages %v missing ./internal/ci", pkgs)
		}
		if !pkgMap["./internal/docs"] {
			t.Errorf("packages %v missing ./internal/docs", pkgs)
		}
	})
}
