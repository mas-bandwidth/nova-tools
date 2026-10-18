package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
)

// The onboarding standard (docs/ONBOARDING.md), pinned for this binary: a bare
// command refuses and names its door, the banner's examples run, and
// docs/TESTS.md's first run is compared against what the tool prints, line for
// line, by the one comparator. The documented lines are typed from the
// checkout root; the sitting runs them from this package's directory, so the
// fixture's path is the one word rewritten in the arguments, and no output
// names it.

// sitting runs documented lines against the example checkout in testdata.
func sitting(t *testing.T) func(args []string) onboarding.Result {
	t.Helper()
	testdata, err := filepath.Abs("testdata")
	require.NoError(t, err)
	stand := strings.NewReplacer("./cmd/nova-tla/testdata/", testdata+"/")
	return func(args []string) onboarding.Result {
		local := make([]string, len(args))
		for i, a := range args {
			local[i] = stand.Replace(a)
		}
		r := call(t, nil, local...)
		return onboarding.Result{Code: r.code, Stdout: r.stdout, Stderr: r.stderr}
	}
}

// A bare command refuses in one line, on stderr at exit 2, and names its door.
func TestABareCommandNamesItsDoor(t *testing.T) {
	t.Parallel()
	res := sitting(t)(nil)
	assert.Equal(t, 2, res.Code)
	assert.Empty(t, res.Stdout)
	assert.Equal(t, 1, strings.Count(res.Stderr, "\n"), res.Stderr)
	assert.Contains(t, res.Stderr, "REFUSED")
	assert.Contains(t, res.Stderr, "run: nova-tla help")
}

// The banner's example block is a first run of at least three lines, and each
// runs at exit 0 against the example checkout: no java, nothing written.
func TestUsageBannerExamplesRun(t *testing.T) {
	t.Parallel()
	run := sitting(t)
	banner := run([]string{"help"})
	require.Equal(t, 0, banner.Code, banner.Stderr)
	examples, err := onboarding.ExampleLines(banner.Stdout, "nova-tla")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(examples), onboarding.MinExampleCommands, "the example block: %q", examples)
	for _, ex := range examples {
		fields, err := onboarding.SplitShell(ex)
		require.NoError(t, err)
		res := run(fields[1:])
		assert.Equal(t, 0, res.Code, "the example %q: %s", ex, res.Stderr)
		assert.NotEmpty(t, res.Stdout, "the example %q printed no result", ex)
	}
}

// The docs/TESTS.md first run is EXECUTED, every command in order, and its
// whole output compared with the block.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-tla")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-tla", lines)
	require.NoError(t, err)
	run := sitting(t)
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		got = append(got, run(s.Args))
	}
	assert.Empty(t, onboarding.CompareTranscript(steps, got, nil))
}
