package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/update"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The first run needs this binary and Go on PATH. It runs in an empty directory: the
// example lines write their own manifest there, never into the checkout.
func TestExecutableFirstRun(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "TESTS.md"))
	require.NoError(t, err)
	t.Chdir(t.TempDir())
	var banner bytes.Buffer
	update.Main("nova-update", []string{"help"}, "", &banner, &banner)
	examples, err := onboarding.ExampleLines(banner.String(), "nova-update")
	require.NoError(t, err)
	for _, line := range examples {
		var out, errs bytes.Buffer
		code := update.Main("nova-update", strings.Fields(line)[1:], "", &out, &errs)
		require.NotEqual(t, 2, code, "%s refused: %s", line, errs.String())
	}
	transcript, err := onboarding.FirstRun(string(doc), "nova-update")
	require.NoError(t, err)
	var wanted, actual []string
	var out, errs bytes.Buffer
	for _, line := range transcript {
		if strings.HasPrefix(line, "$ ") {
			{
				c := update.Main("nova-update", strings.Fields(line)[2:], "", &out, &errs)
				require.Equal(t, 0, c, "first run: %d %s", c, errs.String())
			}
		} else if s := firstRunShape(line); s != "" {
			wanted = append(wanted, s)
		}
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if s := firstRunShape(line); s != "" {
			actual = append(actual, s)
		}
	}
	require.Equal(t, strings.Join(wanted, "\n"), strings.Join(actual, "\n"), "document shape %v differs from run %v", wanted, actual)
}
func TestMissingIndependentFlagsAreNamedTogether(t *testing.T) {
	t.Parallel()

	var out, errs bytes.Buffer
	c := update.Main("nova-update", []string{"report", "--send"}, "", &out, &errs)
	require.Equal(t, 2, c, fmt.Sprint(c))
	for _, flag := range []string{"--file", "--as", "--to", "--bus", "--remote", "--branch"} {
		require.Contains(t, errs.String(), flag, "missing "+flag+": "+errs.String())
	}
	require.LessOrEqual(t, strings.Count(errs.String(), "\n"), 2, "refusal printed a banner")
}

// The `### First run` block under docs/TESTS.md's `## nova-update` section is
// EXECUTED here: the documented command is run and its whole output is compared
// with the block written under it -- same number of lines, same lines, same
// order. TestExecutableFirstRun above reduces each line to its field names, so
// it passes on an abridged or reordered transcript; this test keeps the whole
// promise.
func TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "TESTS.md"))
	require.NoError(t, err)
	t.Chdir(t.TempDir())
	lines, err := onboarding.FirstRun(string(raw), "nova-update")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-update", lines)
	require.NoError(t, err)
	require.NotEmpty(t, steps, "the `### First run` block holds no nova-update command; this test would pass by running nothing")
	for _, p := range onboarding.Execute(steps, runDocumented(t), firstRunNorms(t)...) {
		assert.Fail(t, fmt.Sprint(p))
	}
}

// firstRunNorms declares the five values in this transcript that belong to the
// RUN or to the BENCH rather than to the document. Every other value on every
// line is compared as written.
//
//   - `at=` is the instant the report was taken. It goes through
//     onboarding.Instant, which PARSES the value, so an impossible stamp the
//     tool printed is still a finding rather than a value a norm erased.
//   - `took=` is how long this run's version reads took.
//   - `version=`, `raw=` and `path=` describe this machine's Go installation.
//     The fixture (`testdata/example.tsv`) runs `go version`, so its release,
//     platform and executable path can differ from the documented transcript.
//
// What is NOT declared is the point of comparing line for line: the number of
// lines, their order, and `entries=`, `kinds=`, `checked=`, `known=`,
// `unknown=`, `changed=`, `sent=` and every file name all stay compared, so a
// dropped, reordered or abridged transcript is red.
func firstRunNorms(t *testing.T) []onboarding.Norm {
	t.Helper()
	duration, err := onboarding.Elide(
		"took= (the wall time this run's reads took)",
		`took=[0-9][^\s]*`,
		"took=<the wall time this run's reads took>",
	)
	require.NoError(t, err)
	version, err := onboarding.Elide(
		"version= (the release this bench's version command answers)",
		`version=[^\s]+`,
		"version=<the release this bench's version command answers>",
	)
	require.NoError(t, err)
	rawVersion, err := onboarding.Elide(
		"raw= (this bench's version command printed)",
		`raw=[^\s]+`,
		"raw=<this bench's version command printed>",
	)
	require.NoError(t, err)
	path, err := onboarding.Elide(
		"path= (the executable this bench found on PATH)",
		`path=[^\s]+`,
		"path=<the executable this bench found on PATH>",
	)
	require.NoError(t, err)
	return []onboarding.Norm{onboarding.Instant("at"), duration, version, rawVersion, path}
}

// runDocumented calls this binary's own entry point with the documented
// arguments. nova-update reads no stdin and this transcript has no `< path`
// redirect, so there is no stream to open.
func runDocumented(t *testing.T) onboarding.Runner {
	t.Helper()
	return func(s onboarding.Step) (onboarding.Result, error) {
		var out, errb bytes.Buffer
		code := update.Main("nova-update", s.Args, "", &out, &errb)
		return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
	}
}

// repoRoot is the checkout root: this package sits two directories under it.
// It is resolved rather than assumed so that a failure names a path.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	{
		_, err := os.Stat(filepath.Join(root, "docs", "TESTS.md"))
		require.NoError(t, err, "docs/TESTS.md is not under %s: %v", root, err)
	}
	return root
}

// firstRunShape is a line's event and field names, its values dropped.
func firstRunShape(line string) string { return onboarding.Shape(line) }
