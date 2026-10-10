//go:build perf

package main

import "testing"

// TestEveryVerbsHelpIsWithinTheBudget is the perf tier's run of the verb-help test:
// under -tags perf, internal/testverbhelp holds every verb's help to its wall-clock
// budget (budget_perf.go), and this name exists only under the tag, so the perf job's
// selector (internal/pkgselect.PerfRuns) schedules it. The unit tier runs the same
// checks with no clock.
func TestEveryVerbsHelpIsWithinTheBudget(t *testing.T) {
	t.Parallel()
	t.Run("every verb", TestEveryVerbAnswersHelpAndTouchesNothing)
}
