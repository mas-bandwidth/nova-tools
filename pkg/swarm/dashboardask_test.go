package swarm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLintDashboardAskRefusesAnUnaskedPageChange pins the dashboard-ask lint: a card whose
// PATHS: covers the sprint dashboard page is refused unless its SOURCE: line quotes the owner
// asking for that visible change, dated.
func TestLintDashboardAskRefusesAnUnaskedPageChange(t *testing.T) {
	t.Parallel()
	card := func(paths, source string) []byte {
		b := "tier: flash\nPATHS: " + paths + "\n"
		if source != "" {
			b += "SOURCE: " + source + "\n"
		}
		return []byte(b + "\nTHE TASK: change the thing.\n")
	}

	noQuote := card("internal/sprintdash/page/", "")
	fs := LintDashboardAsk(noQuote)
	require.Len(t, fs, 1)
	assert.Equal(t, DashboardAskCheck, fs[0].Check)
	assert.Contains(t, fs[0].Excerpt, "PATHS reaches the dashboard page")

	dated := card("internal/sprintdash/page/", `the owner, 2026-10-11: "add the seat chip"`)
	assert.Empty(t, LintDashboardAsk(dated))
}
