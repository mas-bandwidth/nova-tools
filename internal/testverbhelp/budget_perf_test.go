//go:build perf

package testverbhelp

import (
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestTheHelpBudgetNamesASlowHelp is the budget's own witness in the perf tier: a help
// slower than Budget on every one of its five runs is named, a quick one is not.
func TestTheHelpBudgetNamesASlowHelp(t *testing.T) {
	t.Parallel()
	slow := func([]string, io.Writer, io.Writer) int { time.Sleep(Budget + 20*time.Millisecond); return 0 }
	quick := func([]string, io.Writer, io.Writer) int { return 0 }
	assert.Contains(t, overBudget(slow, []string{"verb", "-h"}), "help ran something")
	assert.Empty(t, overBudget(quick, []string{"verb", "-h"}))
}
