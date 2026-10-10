package testverbhelp

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The help budget (budget_perf.go) fires only where the perf job runs a test, and that
// job runs by NAME the tests only the perf tag adds (pkg/pkgselect.PerfRuns). A
// budget reached only through the tools' untagged help tests would run nowhere. So
// every tool whose help test goes through Check carries, behind the perf tag and only
// there, TestEveryVerbsHelpIsWithinTheBudget, which runs that help test. This reads the
// tools' test files; pkg/pkgselect's functional test runs the selector itself.
func TestEveryToolsHelpBudgetHasATestThePerfJobSchedules(t *testing.T) {
	t.Parallel()
	const witness = "TestEveryVerbsHelpIsWithinTheBudget"
	perfLine := regexp.MustCompile(`(?m)^//go:build .*\bperf\b`)
	declares := regexp.MustCompile(`(?m)^func ` + witness + `\(t \*testing\.T\) \{\n\tt\.Parallel\(\)\n\tt\.Run\("every verb", TestEveryVerbAnswersHelpAndTouchesNothing\)`)
	dirs, err := filepath.Glob(filepath.Join("..", "..", "cmd", "*"))
	require.NoError(t, err)
	tools := 0
	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
		require.NoError(t, err)
		checks, tagged, untagged := false, 0, 0
		for _, f := range files {
			b, err := os.ReadFile(f)
			require.NoError(t, err)
			src := string(b)
			checks = checks || strings.Contains(src, "testverbhelp.Check(")
			if !strings.Contains(src, "func "+witness+"(") {
				continue
			}
			if perfLine.MatchString(src) && declares.MatchString(src) {
				tagged++
			} else {
				untagged++
			}
		}
		if !checks {
			continue
		}
		tools++
		tool := filepath.Base(dir)
		assert.Equal(t, 1, tagged, "%s: its help test goes through testverbhelp.Check, so a perf-tagged %s that runs it is what the perf job schedules", tool, witness)
		assert.Zero(t, untagged, "%s: %s outside the perf tag is not a name only the tag adds, and the perf job would not select it", tool, witness)
	}
	assert.NotZero(t, tools, "no tool's help test goes through Check; this test checks nothing")
}
