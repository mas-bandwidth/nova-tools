//go:build functional

package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). The rest of the file's tests stay in the
// unit tier.

// TestFakeRunnerRecordsItsArgv: the `record` step writes the runner's whole argv, one element
// per line, which is how a fixture proves WHICH command the batch ran and with what arguments
// -- a `--runner`'s five, or the self's `native` verb (issue #636).
func TestFakeRunnerRecordsItsArgv(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	out := filepath.Join(dir, "argv")
	runner := runnerDoing(t, dir, "recorder", runnerStep{Op: "record", Path: out})
	card := filepath.Join(dir, "card.md")
	root := filepath.Join(dir, "root")
	cmd := exec.Command(runner, "card-f", "1", "m", card, root, "unmetered")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "the recording runner: %v\n%s", err, output)
	want := strings.Join([]string{runner, "card-f", "1", "m", card, root, "unmetered"}, "\n") + "\n"
	got := string(readTestFile(t, out))
	require.Equal(t, want, got, "the recorded argv is %q, want %q", got, want)
}

// TestFakeRunnerPublishesAWholeFileWriteAtomically: a `write` step must not create the
// target empty and fill it in. result-after-deadline waits for RESULT.md to EXIST and then
// fires the deadline; that window scored line1-mismatch on the Studio once the runner was
// fast enough to be caught mid-write (2026-09-17, a9c10034). The batch tests drive this
// helper as a subprocess and used to assert only the lines it produced, so reverting the
// temp+rename left them green (#2026).
func TestFakeRunnerPublishesAWholeFileWriteAtomically(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("testdata", "fakerunner", "main.go"))
	require.NoError(t, err)
	src := string(raw)
	const sig = "func (r *runner) writeFile(path, body string, appendTo bool) {"
	i := strings.Index(src, sig)
	require.GreaterOrEqual(t, i, 0, "testdata/fakerunner/main.go no longer has writeFile; the atomic whole-file write lived there")
	fn := src[i:]
	if j := strings.Index(fn[len(sig):], "\nfunc "); j >= 0 {
		fn = fn[:len(sig)+j]
	}
	require.Contains(t, fn, "os.CreateTemp", "writeFile no longer publishes a whole-file write by temp+rename; result-after-deadline can observe RESULT.md created and still empty")
	require.Contains(t, fn, "os.Rename", "writeFile no longer publishes a whole-file write by temp+rename; result-after-deadline can observe RESULT.md created and still empty")

	dir := t.TempDir()
	secret := filepath.Join(dir, "outside")
	require.NoError(t, os.WriteFile(secret, []byte("a secret the write must not touch\n"), 0o644))
	dest := filepath.Join(dir, "RESULT.md")
	if err := os.Link(secret, dest); err != nil {
		t.Logf("hard link unavailable (%v); writeFile's source still names CreateTemp and Rename", err)
		return
	}
	runner := runnerDoing(t, dir, "writer", runnerStep{Op: "write", Path: dest, Body: "the published body"})
	card := filepath.Join(dir, "card.md")
	root := filepath.Join(dir, "root")
	cmd := exec.Command(runner, "card-f", "1", "m", card, root, "unmetered")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "the writing runner: %v\n%s", err, output)
	got := string(readTestFile(t, secret))
	require.Equal(t, "a secret the write must not touch\n", got, "the write landed in place through a planted hard link and overwrote the other name: %q", got)
	got = string(readTestFile(t, dest))
	require.Equal(t, "the published body\n", got, "the published file is %q, want the body", got)
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "reading %s: %v", path, err)
	return string(raw)
}
