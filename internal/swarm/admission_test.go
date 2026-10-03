package swarm

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardtree"
)

func TestAdmissionBindsNamesTheTokensAddRefuses(t *testing.T) {
	t.Parallel()
	for _, check := range []string{
		"result-first", "clone-step", "steps-numbered", "red-test", "test-command",
		"deadline", "files-named", "scratch-absolute", "no-parent-path", "no-sandbox",
		"result-last", "kind-declared", "paths-declared", "test-named", "paused",
		"depends-on", "paths-at-base", "placeholder", "size",
	} {
		assert.False(t, AdmissionBinds(check), check)
	}
	for _, check := range []string{
		"rule-worktree", "rule-report-not-done", "step-redis-server", "step-go-clean",
		"step-go-test-timeout", EmptyCardCheck, LibrariesConsideredRule,
		"steps-nested", "tree-step", "script-step",
	} {
		assert.True(t, AdmissionBinds(check), check)
	}
}

func TestAdmissionContractNamesTheSixGeneralRules(t *testing.T) {
	t.Parallel()
	require.Len(t, DefaultChildRules, 6)
	require.Contains(t, AdmissionContract, "six general rules")
	require.NotContains(t, AdmissionContract, "`")
	require.NotContains(t, AdmissionContract, "LINT DRIFT")
}

// The discriminating fixture fails the shape check on line 1 and carries the
// six general rules, so a bare lint drifts while admission has nothing to refuse.
func TestAdmissionMismatchFixtureCarriesTheGeneralRules(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/admission-mismatch.md")
	require.NoError(t, err)
	line, _, ok := strings.Cut(string(raw), "\n")
	require.True(t, ok)
	require.False(t, IsCardContractLine(line))
	require.Empty(t, LintCardChildWith(raw, DefaultChildRules))
	require.False(t, cardtree.Parse(string(raw)).IsTree())
}
