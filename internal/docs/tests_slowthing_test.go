package docs

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci/slowtests"
)

// TestSlowThing pins the first run of docs/TESTS.md's nova-ci section: a
// package whose total elapsed time is over the budget prints one CI-SLOW line
// naming the package, the total, the budget, and the few test-level rows that
// spent it, worst first.
//
// docs/TESTS.md line 1025: "Each package's total is its package-level Elapsed,
// and the slowest= list names the few test-level rows that spent it."
func TestSlowThing(t *testing.T) {
	t.Parallel()

	fixture, err := os.ReadFile("../../cmd/nova-ci/testdata/example-events.jsonl")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}

	events, err := slowtests.Parse(strings.NewReader(string(fixture)))
	if err != nil {
		t.Fatalf("slowtests.Parse: %v", err)
	}

	report := slowtests.Sum(events, 60*time.Second)
	if report.ExitCode() != 2 {
		t.Fatalf("exit code = %d, want 2; the package is over budget", report.ExitCode())
	}

	lines := report.OverLines()
	if len(lines) != 1 {
		t.Fatalf("over lines = %d, want 1: %v", len(lines), lines)
	}

	want := "CI-SLOW package=github.com/mas-bandwidth/nova-tools/internal/example seconds=65.1s budget=60s slowest=TestSlowThing:63.4s,TestAlsoSlow:1.5s"
	if lines[0] != want {
		t.Errorf("over line = %q, want %q", lines[0], want)
	}
}
