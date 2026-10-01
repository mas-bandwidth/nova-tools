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
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SLOW: 7.5 s on hetzner at dev 64b9bec48, over the five-second line.
// status is a verb to be GLANCED at, and at three hundred quarantined surfaces it was
// three hundred and one lines.
func TestStatusCountsAllAndListsAtMostMax(t *testing.T) {
	t.Parallel()

	box := crowdedBox(t, 300)
	exit, stdout, stderr := runFuse(t, "status", "--box", box)
	require.Equal(t, 0, exit, "exit = %d, want 0; stderr: %s", exit, stderr)
	// The count line, twenty listed, one MORE line.
	got := countLines(stdout)
	assert.Equal(t, int(bounded.Default+2), got, "stdout is %d lines, want %d listed + count + MORE:\n%s", got, bounded.Default, stdout)
	// THE COUNT IS NEVER CAPPED: this is the number the verb exists to report.
	assert.Contains(t, stdout, "STATUS OK lockdown=clear quarantines=300", "the count line does not carry the whole total:\n%s", stdout)
	assert.Contains(t, stdout, "STATUS MORE kind=quarantine shown=20 total=300", "no MORE line naming the total:\n%s", stdout)
	assert.Contains(t, stdout, "--max", "the MORE line names no remedy:\n%s", stdout)
}
