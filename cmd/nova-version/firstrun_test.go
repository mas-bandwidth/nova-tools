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
// directory: the manifest name the example lines carry is resolved under
// t.TempDir(), the per-test seam the serial-tests ledger names
// (internal/ci/testdata/serial-tests_allowlist.txt), so the manifest is written
// and read there, never in the checkout, and the process working directory is
// never changed.
func TestExecutableFirstRun(t *testing.T) {
	t.Parallel()

	doc, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "TESTS.md"))
	require.NoError(t, err, err)
	dir := t.TempDir()
	var banner bytes.Buffer
	update.Main("nova-version", []string{"help"}, "", &banner, &banner)
	examples, err := onboarding.ExampleLines(banner.String(), "nova-version")
	require.NoError(t, err, err)
	for _, line := range examples {
		var out, errs bytes.Buffer
		code := update.Main("nova-version", firstRunArgs(strings.Fields(line)[1:], dir), "", &out, &errs)
		require.NotEqual(t, 2, code, "%s refused: %s", line, errs.String())
	}
	transcript, err := onboarding.FirstRun(string(doc), "nova-version")
	require.NoError(t, err, err)
	var wanted, actual []string
	var out, errs bytes.Buffer
	for _, line := range transcript {
		if strings.HasPrefix(line, "$ ") {
			{
				c := update.Main("nova-version", firstRunArgs(strings.Fields(line)[2:], dir), "", &out, &errs)
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
	c := update.Main("nova-version", []string{"report", "--send"}, "", &out, &errs)
	require.Equal(t, 2, c, fmt.Sprint(c))
	for _, flag := range []string{"--file", "--as", "--to"} {
		require.Contains(t, errs.String(), flag, "missing "+flag+": "+errs.String())
	}
	require.LessOrEqual(t, strings.Count(errs.String(), "\n"), 2, "refusal printed a banner")
}

// firstRunShape is a line's event and field names, its values dropped.
func firstRunShape(line string) string { return onboarding.Shape(line) }

// TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine executes the
// `### First run` block of docs/TESTS.md for nova-version -- every command, in
// order -- and compares each command's whole output with the block written
// under it: same number of lines, same lines, same order. The existing
// TestExecutableFirstRun asks whether each line is the right SHAPE, which keeps
// the words and drops the count and the order; this one keeps the whole
// promise.
func TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "TESTS.md"))
	require.NoError(t, err, err)
	dir := t.TempDir()
	lines, err := onboarding.FirstRun(string(raw), "nova-version")
	require.NoError(t, err, err)
	steps, err := onboarding.Steps("nova-version", lines)
	require.NoError(t, err, err)
	require.NotEmpty(t, steps, "the `### First run` block holds no nova-version command; this test would pass by running nothing")
	// The transcript declares four values as owned by the RUN or the BENCH
	// rather than by the document, and nothing else: the instant of the run
	// (`at=`) and its duration (`took=`), and the three fields of the
	// `REPORT TOOL` line that say which Go THIS machine runs (`version=`),
	// what its `version` command printed (`raw=`) and where it is installed
	// (`path=`). The document records the Studio's arm64 Go; a bench runs its
	// own, so those three cannot be compared as written. Every other value --
	// the file, the counts, the kinds, the bounds -- is compared exactly. The
	// run's own directory is declared too: the runner resolves the documented
	// name versions.tsv under the test's t.TempDir, and onboarding.Path
	// reduces both sides to the documented spelling.
	norms := []onboarding.Norm{
		onboarding.Instant("at"),
		elide(t, "took= (the duration of this run)", `took=[^ ]+`, "took=<the duration of this run>"),
		elide(t, "version= (the Go this bench runs)", `version=[^ ]+`, "version=<the Go this bench runs>"),
		elide(t, "raw= (what this bench's version command printed)", `raw=[^ ]+`, "raw=<what this bench's version command printed>"),
		elide(t, "path= (where this bench's Go is installed)", `path=[^ ]+`, "path=<where this bench's Go is installed>"),
		onboarding.Path("versions.tsv", filepath.Join(dir, "versions.tsv")),
	}
	for _, p := range onboarding.Execute(steps, runVersionDocumented(t, dir), norms...) {
		assert.Fail(t, fmt.Sprint(p))
	}
}

// elide declares one normalisation this package has no constructor for. The
// name is what a reader of a failing test is told is not compared.
func elide(t *testing.T, name, pattern, as string) onboarding.Norm {
	t.Helper()
	n, err := onboarding.Elide(name, pattern, as)
	require.NoError(t, err, err)
	return n
}

// runVersionDocumented calls this tool's own entry point with the documented
// arguments, resolving the manifest name the block writes and reads against
// dir so the sitting needs no process-wide working directory. A redirect would
// be a transcript for a tool that reads stdin, and nova-version reads none: it
// is refused here rather than silently dropped, so a future document that adds
// one fails loudly instead of running a different command than the reader
// typed.
func runVersionDocumented(t *testing.T, dir string) onboarding.Runner {
	t.Helper()
	return func(s onboarding.Step) (onboarding.Result, error) {
		if s.Stdin != "" {
			return onboarding.Result{}, fmt.Errorf("the transcript redirects %q into nova-version, which reads no stdin", s.Stdin)
		}
		var out, errb bytes.Buffer
		code := update.Main("nova-version", firstRunArgs(s.Args, dir), "", &out, &errb)
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
