package docs

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci/slowtests"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err, "fixture: %v", err)

	events, err := slowtests.Parse(strings.NewReader(string(fixture)))
	require.NoError(t, err, "slowtests.Parse: %v", err)

	report := slowtests.Sum(events, 60*time.Second)
	require.Equal(t, 1, report.ExitCode(), "exit code = %d, want 1; the package is over budget", report.ExitCode())

	lines := report.OverLines()
	require.Len(t, lines, 1, "over lines = %d, want 1: %v", len(lines), lines)

	want := "CI-SLOW package=github.com/mas-bandwidth/nova-tools/internal/example seconds=65.1s budget=60s slowest=TestSlowThing:63.4s,TestAlsoSlow:1.5s"
	assert.Equal(t, want, lines[0], "over line = %q, want %q", lines[0], want)
}
