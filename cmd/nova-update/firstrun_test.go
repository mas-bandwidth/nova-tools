package main

import (
	"bytes"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/update"
	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The first run needs the binary alone, so it runs in the test's own empty
// directory: the manifest path the example lines name is resolved under
// t.TempDir(), so nothing is written into the checkout and the process working
// directory is never changed. t.TempDir for a path is the seam the serial-tests
// ledger names (internal/ci/testdata/serial-tests_allowlist.txt).
func TestExecutableFirstRun(t *testing.T) {
	t.Parallel()

	doc, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "TESTS.md"))
	require.NoError(t, err, err)
	dir := t.TempDir()
	var banner bytes.Buffer
	update.Main("nova-update", []string{"help"}, "", &banner, &banner)
	examples, err := onboarding.ExampleLines(banner.String(), "nova-update")
	require.NoError(t, err, err)
	for _, line := range examples {
		var out, errs bytes.Buffer
		code := update.Main("nova-update", firstRunArgs(strings.Fields(line)[1:], dir), "", &out, &errs)
		require.NotEqual(t, 2, code, "%s refused: %s", line, errs.String())
	}
	transcript, err := onboarding.FirstRun(string(doc), "nova-update")
	require.NoError(t, err, err)
	var wanted, actual []string
	var out, errs bytes.Buffer
	for _, line := range transcript {
		if strings.HasPrefix(line, "$ ") {
			{
				c := update.Main("nova-update", firstRunArgs(strings.Fields(line)[2:], dir), "", &out, &errs)
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
	for _, flag := range []string{"--file", "--as", "--to"} {
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
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "TESTS.md"))
	require.NoError(t, err, err)
	dir := t.TempDir()
	lines, err := onboarding.FirstRun(string(raw), "nova-update")
	require.NoError(t, err, err)
	steps, err := onboarding.Steps("nova-update", lines)
	require.NoError(t, err, err)
	require.NotEmpty(t, steps, "the `### First run` block holds no nova-update command; this test would pass by running nothing")
	norms := append(firstRunNorms(t), onboarding.Path("versions.tsv", filepath.Join(dir, "versions.tsv")))
	for _, p := range onboarding.Execute(steps, runDocumented(t, dir), norms...) {
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
//   - `version=`, `raw=` and `path=` are the `go version` THIS bench answers.
//     The documented transcript was cut on the Studio (go1.27.1, darwin/arm64,
//     /opt/homebrew/bin/go); the same command on a Linux bench answers a
//     different release, a different GOOS/GOARCH and a different executable
//     path. The fixture (`testdata/example.tsv`) declares `go version` as the
//     installed command, so these three fields are the bench's by construction
//     and no run of this test on any machine could reproduce the Studio's.
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
	require.NoError(t, err, err)
	version, err := onboarding.Elide(
		"version= (the release this bench's version command answers)",
		`version=[^\s]+`,
		"version=<the release this bench's version command answers>",
	)
	require.NoError(t, err, err)
	rawVersion, err := onboarding.Elide(
		"raw= (this bench's version command printed)",
		`raw=[^\s]+`,
		"raw=<this bench's version command printed>",
	)
	require.NoError(t, err, err)
	path, err := onboarding.Elide(
		"path= (the executable this bench found on PATH)",
		`path=[^\s]+`,
		"path=<the executable this bench found on PATH>",
	)
	require.NoError(t, err, err)
	return []onboarding.Norm{onboarding.Instant("at"), duration, version, rawVersion, path}
}

// runDocumented calls this binary's own entry point with the documented
// arguments, resolving the manifest name the block writes and reads against
// dir so the sitting needs no process-wide working directory. nova-update reads
// no stdin and this transcript has no `< path` redirect, so there is no stream
// to open.
func runDocumented(t *testing.T, dir string) onboarding.Runner {
	t.Helper()
	return func(s onboarding.Step) (onboarding.Result, error) {
		var out, errb bytes.Buffer
		code := update.Main("nova-update", firstRunArgs(s.Args, dir), "", &out, &errb)
		return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
	}
}

// firstRunArgs resolves the one relative path the documented first run names
// (versions.tsv) under dir, the test's own t.TempDir, so the manifest is
// written and read there instead of in the process working directory.
func firstRunArgs(args []string, dir string) []string {
	resolved := make([]string, len(args))
	for i, a := range args {
		if a == "versions.tsv" {
			a = filepath.Join(dir, "versions.tsv")
		}
		resolved[i] = a
	}
	return resolved
}

// repoRoot is the checkout root: this package sits two directories under it.
// It is resolved rather than assumed so that a failure names a path.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err, err)
	{
		_, err := os.Stat(filepath.Join(root, "docs", "TESTS.md"))
		require.NoError(t, err, "docs/TESTS.md is not under %s: %v", root, err)
	}
	return root
}

// firstRunShape is a line's event and field names, its values dropped.
func firstRunShape(line string) string { return onboarding.Shape(line) }
