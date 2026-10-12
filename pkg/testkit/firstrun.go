package testkit

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MainFunc is a tool's entry point in the shape the update/version binaries
// share: the tool's own name, the arguments after it, the build stamp (empty
// unless a release stamped one), and the two output streams.
type MainFunc func(name string, args []string, stamp string, stdout, stderr io.Writer) int

// RepoRoot is the checkout root: both a command package (cmd/<tool>) and this
// package (pkg/testkit) sit two directories under it, so it is resolved
// relative to the test's working directory and checked against a file only the
// root holds, so that a failure names a path rather than an assumption.
func RepoRoot(t testing.TB) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	{
		_, err := os.Stat(filepath.Join(root, "docs", "TESTS.md"))
		require.NoError(t, err, "docs/TESTS.md is not under %s: %v", root, err)
	}
	return root
}

// FirstRunArgs resolves the one relative path a documented first run names
// (versions.tsv) under dir, the test's own t.TempDir, so the manifest is
// written and read there instead of in the process working directory.
func FirstRunArgs(args []string, dir string) []string {
	resolved := make([]string, len(args))
	for i, a := range args {
		if a == "versions.tsv" {
			a = filepath.Join(dir, "versions.tsv")
		}
		resolved[i] = a
	}
	return resolved
}

// ExecutableFirstRun runs a tool's documented first run in the test's own
// temporary directory, so the manifest the example lines name is written and
// read under t.TempDir and the process working directory is never changed: the
// example block the banner carries is RUN rather than read, and every line of
// the `### First run` transcript of docs/TESTS.md is compared with what the
// tool prints, field name by field name (ONBOARDING point 5a and 5c). It is
// the one body the command packages' TestExecutableFirstRun each keep, and the
// two call it with their own tool name and entry point.
func ExecutableFirstRun(t *testing.T, tool string, main MainFunc) {
	t.Helper()

	doc, err := os.ReadFile(filepath.Join(RepoRoot(t), "docs", "TESTS.md"))
	require.NoError(t, err, err)
	dir := t.TempDir()
	var banner bytes.Buffer
	main(tool, []string{"help"}, "", &banner, &banner)
	examples, err := onboarding.ExampleLines(banner.String(), tool)
	require.NoError(t, err, err)
	for _, line := range examples {
		var out, errs bytes.Buffer
		code := main(tool, FirstRunArgs(strings.Fields(line)[1:], dir), "", &out, &errs)
		require.NotEqual(t, 2, code, "%s refused: %s", line, errs.String())
	}
	transcript, err := onboarding.FirstRun(string(doc), tool)
	require.NoError(t, err, err)
	var wanted, actual []string
	var out, errs bytes.Buffer
	for _, line := range transcript {
		if strings.HasPrefix(line, "$ ") {
			{
				c := main(tool, FirstRunArgs(strings.Fields(line)[2:], dir), "", &out, &errs)
				require.Equal(t, 0, c, "first run: %d %s", c, errs.String())
			}
		} else if s := onboarding.Shape(line); s != "" {
			wanted = append(wanted, s)
		}
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if s := onboarding.Shape(line); s != "" {
			actual = append(actual, s)
		}
	}
	require.Equal(t, strings.Join(wanted, "\n"), strings.Join(actual, "\n"), "document shape %v differs from run %v", wanted, actual)
}

// VersionLineRun runs a tool's `version` verb and returns the exit code and
// both streams, the one parameter the shared shape check cannot know.
type VersionLineRun func() (code int, stdout, stderr string)

// VersionLineShape asserts that one run of a tool's `version` verb is a single
// terminated line of four fields -- the tool's name, a build identity, the
// bench's GOOS/GOARCH, and the bench's Go release -- and wrote nothing to
// stderr. Asserting only that the output "contains" a version would pass over
// a line broken in two, which is the failure the escaping in pkg/buildinfo
// exists to prevent.
func VersionLineShape(t *testing.T, tool string, run VersionLineRun) {
	t.Helper()

	code, out, errOut := run()
	require.Equal(t, 0, code, "exit %d, want 0\nstderr: %s", code, errOut)
	assert.Zero(t, errOut, "wrote to stderr: %q", errOut)
	line := out
	require.True(t, strings.HasSuffix(line, "\n"), "want exactly one terminated line, got %q", line)
	require.Equal(t, 1, strings.Count(line, "\n"), "want exactly one terminated line, got %q", line)
	fields := strings.Fields(strings.TrimSuffix(line, "\n"))
	require.Len(t, fields, 4, "want 4 fields, got %d: %q", len(fields), line)
	assert.Equal(t, tool, fields[0], "field 1 is the binary's name: got %q", fields[0])
	assert.NotEmpty(t, fields[1], "field 2 is the build identity and is never empty: %q", line)
	assert.False(t, strings.ContainsAny(fields[1], " \t"), "field 2 is one token: %q", line)
	want := runtime.GOOS + "/" + runtime.GOARCH
	assert.Equal(t, want, fields[2], "field 3: got %q, want %q", fields[2], want)
	assert.Equal(t, runtime.Version(), fields[3], "field 4: got %q, want %q", fields[3], runtime.Version())
}
