package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHelpPrintsUsageBeforeTheCalExplanation pins that help reaches the verbs
// before the CAL essay (docs/STANDARD.md, onboarding point 6: what it does,
// how it works, and how to use it come before the usage lines, and the CAL
// explanation follows the flags under its own heading).
func TestHelpPrintsUsageBeforeTheCalExplanation(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runCLI(t, "", "help")
	require.Equalf(t, 0, exit, "help exited %d: %s", exit, stderr)
	usageAt := strings.Index(stdout, "usage:")
	calAt := strings.Index(stdout, "CAL is the top score")
	require.GreaterOrEqualf(t, usageAt, 0, "help has no usage: line:\n%s", stdout)
	require.GreaterOrEqualf(t, calAt, 0, "help has no CAL explanation:\n%s", stdout)
	assert.Lessf(t, usageAt, calAt, "usage: is at %d, after \"CAL is the top score\" at %d; the verbs come first:\n%s", usageAt, calAt, stdout)
}
