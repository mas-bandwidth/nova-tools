//go:build slow

// The tests of this package that cost more than the per-commit run can pay:
// over five seconds each on the Linux bench, or a deadline, wedge or wall-clock
// bound proved by waiting it out. They are behind the `slow` build tag, so
// go-test-cmd and go-test-internal do not build them, and
// .github/workflows/nightly-slow.yml (and `make test-slow`) runs them whole,
// every night. Each carries the measurement that moved it. Nothing here is
// skipped or weakened.

package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
)

// SLOW: 7.5 s on hetzner at dev 64b9bec48, over the five-second line.
// status is a verb to be GLANCED at, and at three hundred quarantined surfaces it was
// three hundred and one lines.
func TestStatusCountsAllAndListsAtMostMax(t *testing.T) {
	t.Parallel()

	box := crowdedBox(t, 300)
	exit, stdout, stderr := runFuse(t, "status", "--box", box)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", exit, stderr)
	}
	// The count line, twenty listed, one MORE line.
	if got := countLines(stdout); got != bounded.Default+2 {
		t.Errorf("stdout is %d lines, want %d listed + count + MORE:\n%s", got, bounded.Default, stdout)
	}
	// THE COUNT IS NEVER CAPPED: this is the number the verb exists to report.
	if !strings.Contains(stdout, "STATUS OK lockdown=clear quarantines=300") {
		t.Errorf("the count line does not carry the whole total:\n%s", stdout)
	}
	if !strings.Contains(stdout, "STATUS MORE kind=quarantine shown=20 total=300") {
		t.Errorf("no MORE line naming the total:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--max") {
		t.Errorf("the MORE line names no remedy:\n%s", stdout)
	}
}
