package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Card ci-slowtests-highlights-unenforced-budget-breach: an over-budget run
// without --enforce must say within_budget=false prominently, with the exact
// enforcing command, in text and in JSON; exit stays 0 (a measurement).
func TestCiSlowtestsHighlightsUnenforcedBudgetBreach(t *testing.T) {
	t.Parallel()

	stdin := "{\"Action\":\"pass\",\"Package\":\"example.com/slow\",\"Elapsed\":61}\n"
	args := []string{"slowtests", "--budget", "60", "--load", "1", "--cpus", "2"}

	code, stdout, _ := runCI(t, args, stdin)
	assert.Equal(t, 0, code, "unenforced breach: exit %d stdout %q, want 0 (measurement, not a failure)", code, stdout)
	assert.Contains(t, stdout, "within_budget=false", "unenforced breach text: stdout %q lacks within_budget=false", stdout)
	assert.Contains(t, stdout, "nova-ci slowtests --enforce", "unenforced breach text: stdout %q lacks the enforcing command", stdout)

	code, stdout, _ = runCI(t, []string{"slowtests", "--budget", "60", "--load", "1", "--cpus", "2", "--json"}, stdin)
	assert.Equal(t, 0, code, "unenforced breach --json: exit %d stdout %q, want 0", code, stdout)
	var got struct {
		Facts struct {
			WithinBudget bool `json:"within_budget"`
		} `json:"facts"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "slowtests --json: stdout %q is not JSON", stdout)
	assert.False(t, got.Facts.WithinBudget, "slowtests --json: within_budget = true, want false on breach; stdout %q", stdout)
	assert.True(t, strings.Contains(stdout, "within_budget"), "slowtests --json: stdout %q lacks within_budget", stdout)
}
