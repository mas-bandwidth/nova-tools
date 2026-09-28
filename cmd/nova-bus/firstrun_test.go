package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// The exemption is gone, and it stays gone: a `### First run` for nova-bus exists in
// the document the walk reads, and no entry in internal/ci's skip list (onboarding_functional_test.go) names this
// tool. Asserted here as well as there because the skip list is one map entry away
// from being back, and the tool the README sends people to first is the worst one to
// excuse.
func TestNovaBusIsNotExemptFromTheOnboardingStandard(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := onboarding.FirstRun(string(raw), "nova-bus"); err != nil {
		t.Fatalf("%v\n(docs/ONBOARDING.md point 5(c))", err)
	}
	// The walk is a functional test since #4372 (it builds and runs every command).
	walk, err := os.ReadFile(filepath.Join("..", "..", "internal", "ci", "onboarding_functional_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	body, _, ok := strings.Cut(string(walk), "func TestEveryCommandMeetsTheOnboardingStandard")
	if !ok {
		t.Fatal("internal/ci/onboarding_functional_test.go no longer holds the walk this test is about")
	}
	if strings.Contains(body, `"nova-bus"`) {
		t.Error("internal/ci/onboarding_functional_test.go's skip list names nova-bus again; the first run it was waiting for is in docs/TESTS.md and is executed by firstrun_functional_test.go")
	}
}
