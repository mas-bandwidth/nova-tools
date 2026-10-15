package swarm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dashCard is a card brief whose header carries paths and source (none when "").
func dashCard(paths, source string) []byte {
	b := "tier: flash\nREPO: mas-bandwidth/nova-sprint\nBASE: main\nPATHS: " + paths + "\n"
	if source != "" {
		b += "SOURCE: " + source + "\n"
	}
	return []byte(b + "\nTHE TASK: change the thing.\n")
}

// The dashboard rule (dashboardask.go; the owner, 2026-10-11: "don't add things to the
// dashboard unless I ask for them"), ported from nova-sprint's copy: a card reaching the
// page's files or the code that draws them is refused without a dated quote of the owner's
// ask on SOURCE, and let through with one; a card elsewhere is never held.
func TestLintDashboardAskRefusesAnUnaskedPageChange(t *testing.T) {
	t.Parallel()
	// the brief's pinned pair: the page with no ask has one finding; with the dated ask, none
	fs := LintDashboardAsk(dashCard("internal/sprintdash/page/", ""))
	require.Len(t, fs, 1)
	assert.Equal(t, DashboardAskCheck, fs[0].Check)
	assert.Equal(t, "dashboard-asked-for", fs[0].Check)
	assert.Contains(t, fs[0].Excerpt, "the card has no SOURCE line")
	assert.Empty(t, LintDashboardAsk(dashCard("internal/sprintdash/page/", `the owner, 2026-10-11: "add the seat chip"`)))

	ask := `the owner, 2026-10-11: "show the orange read cards"`
	for _, paths := range []string{
		"internal/sprintdash/page/app.js",
		"internal/sprintdash/page/index.html,internal/sprintdash/page/app.js",
		"internal/sprintdash/page/",
		"internal/sprintdash/page/new-chip.css",
		"internal/sprintdash/*.go",
		"internal/sprintdash/pageview.go",
		"internal/sprintdash/dashboard.go",
		"internal/sprintdash",
	} {
		// known no: no SOURCE, a SOURCE that is an issue, a quote with no date, a date with no quote
		for _, src := range []string{"", "mas-bandwidth/nova-sprint#80", `the owner: "remove the seat chip"`, "the owner, 2026-10-11, said so"} {
			fs := LintDashboardAsk(dashCard(paths, src))
			require.Len(t, fs, 1, "%s / %q", paths, src)
			assert.Equal(t, DashboardAskCheck, fs[0].Check)
			assert.Contains(t, fs[0].Excerpt, "PATHS reaches the dashboard page")
			assert.Contains(t, fs[0].Excerpt, "NEVER REMOVE THINGS FROM THE DASHBOARD UNLESS I ASK", "the refusal says why")
		}
		// known yes: the owner's ask, dated and quoted
		assert.Empty(t, LintDashboardAsk(dashCard(paths, ask)), paths)
		assert.Empty(t, LintDashboardAsk(dashCard(paths, "the owner, 2026-10-11: “show the orange read cards”")), "curly quotes: %s", paths)
	}
	// NEW reaches it too
	b := []byte("tier: flash\nPATHS: docs/x.md\nNEW: internal/sprintdash/page/seat.js\n\nTHE TASK: x\n")
	assert.Len(t, LintDashboardAsk(b), 1)
	// elsewhere: never held, with or without a source
	for _, paths := range []string{"internal/sprint/tick.go", "internal/sprintdash/pull.go", "cmd/nova-sprint/*.go", "docs/SPEC-SPRINT-DASHBOARD.md"} {
		assert.Empty(t, LintDashboardAsk(dashCard(paths, "")), paths)
	}
	// no PATHS: no card brief
	assert.Empty(t, LintDashboardAsk([]byte("tier: flash\n\nTHE TASK: edit internal/sprintdash/page/app.js\n")))
	// the exported pieces the sprint imports
	assert.True(t, DashboardAsked(ask))
	assert.False(t, DashboardAsked("the owner said so"))
	assert.Equal(t, "internal/sprintdash/page", DashboardPageDir)
	assert.Equal(t, []string{"internal/sprintdash/pageview.go", "internal/sprintdash/dashboard.go"}, DashboardRenderCode)
	assert.Contains(t, DashboardAskRemedy, "SOURCE: the owner, <YYYY-MM-DD>")
}
