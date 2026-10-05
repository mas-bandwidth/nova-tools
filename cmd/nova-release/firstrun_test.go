package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/release"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The `### First run` block of docs/TESTS.md's `## nova-release` section is
// EXECUTED here: every command in order, and its whole output compared with
// the block by the one comparator (SPEC-TOOLWORK.md documents rule 2). The
// transcript's commands need no checkout, no forge and no machine: a first run
// of this tool reads -- the refusal that names every flag cut wants, and the
// flag list of the verb that refusal names.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-release")
	require.NoError(t, err, err)
	steps, err := onboarding.Steps("nova-release", lines)
	require.NoError(t, err, err)
	require.NotEmpty(t, steps, "the `### First run` block holds no nova-release command; this test would pass by running nothing")
	var got []onboarding.Result
	for _, s := range steps {
		var out, errs bytes.Buffer
		code := release.Main("nova-release", s.Args, "", &out, &errs)
		got = append(got, onboarding.Result{Code: code, Stdout: out.String(), Stderr: errs.String()})
	}
	assert.Empty(t, onboarding.CompareTranscript(steps, got, nil))
}

// The help's `example:` block is a first run (docs/ONBOARDING.md point 6), and
// each line runs as printed, exit 0, writing nothing: the block is the reading
// a stranger does before the verbs that need a checkout and a forge. The
// version line is this build's own record, so its first word is checked, not
// its stamp (internal/buildinfo writes and reads the one line shape).
func TestHelpExampleLinesRunAsPrinted(t *testing.T) {
	t.Parallel()
	documented := []string{"nova-release help", "nova-release cut -h", "nova-release version"}
	var banner bytes.Buffer
	require.Zero(t, release.Main("nova-release", []string{"help"}, "", &banner, &banner))
	examples, err := onboarding.ExampleLines(banner.String(), "nova-release")
	require.NoError(t, err, err)
	require.Equal(t, documented, examples, "the banner's example block is not the first run this test names")
	for _, line := range examples {
		var out, errs bytes.Buffer
		code := release.Main("nova-release", strings.Fields(line)[1:], "", &out, &errs)
		assert.Zero(t, code, "the example %q exits %d: %s", line, code, errs.String())
		assert.True(t, strings.HasPrefix(out.String(), "nova-release") || strings.HasPrefix(out.String(), "usage: nova-release"), "the example %q prints no line of its own: %q", line, out.String())
		assert.Empty(t, errs.String(), "the example %q writes to stderr: %q", line, errs.String())
	}
}
