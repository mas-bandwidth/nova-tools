package testkit_test

import (
	"fmt"
	"io"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// echo is an entry point that prints its arguments and stdin, and exits with
// the code named by its first argument.
var echo = testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	in, _ := io.ReadAll(stdin)
	fmt.Fprintf(stdout, "args=%q stdin=%q", args, in)
	fmt.Fprint(stderr, "err")
	if len(args) > 0 && args[0] == "fail" {
		return 2
	}
	return 0
})

func TestRunCapturesCodeAndBothStreams(t *testing.T) {
	t.Parallel()
	assert.Equal(t, testkit.Result{Code: 2, Stdout: `args=["fail" "x"] stdin=""`, Stderr: "err"}, echo.Run("fail", "x"))
	assert.Equal(t, `args=["a"] stdin="in"`, echo.RunIn("in", "a").Stdout)
	assert.Equal(t, `args=[] stdin="in"`, echo.OKIn(t, "in").Stdout)
}

func TestOKFailsTheTestOnANonzeroExit(t *testing.T) {
	t.Parallel()
	rec := &recorder{TB: t}
	runs(rec, func() { echo.OK(rec, "fail") })
	assert.True(t, rec.failed, "OK passed a run that exited 2")
	assert.Contains(t, rec.msg, `stderr="err"`, "OK's failure does not name the streams")
	rec = &recorder{TB: t}
	runs(rec, func() { echo.OK(rec, "ok") })
	assert.False(t, rec.failed, "OK failed a run that exited 0: %s", rec.msg)
}

func TestNoStdinGivesTheEntryPointAnEmptyReader(t *testing.T) {
	t.Parallel()
	var out, errb capture
	code := echo.NoStdin()([]string{"v"}, &out, &errb)
	assert.Equal(t, 0, code)
	assert.Equal(t, `args=["v"] stdin=""`, string(out))
}

func TestFilesRoundTripAndMakeParents(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "a", "b", "c.json")
	testkit.WriteFile(t, path, `{"text":"words"}`)
	assert.Equal(t, `{"text":"words"}`, testkit.ReadFile(t, path))
	got := testkit.ReadJSON[struct {
		Text string `json:"text"`
	}](t, path)
	assert.Equal(t, "words", got.Text)
}

func TestReadFailsTheTestOnAMissingOrBadFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rec := &recorder{TB: t}
	runs(rec, func() { testkit.ReadFile(rec, filepath.Join(dir, "missing")) })
	assert.True(t, rec.failed, "ReadFile passed a missing file")
	bad := filepath.Join(dir, "bad.json")
	testkit.WriteFile(t, bad, "{")
	rec = &recorder{TB: t}
	runs(rec, func() { testkit.ReadJSON[map[string]any](rec, bad) })
	assert.True(t, rec.failed, "ReadJSON passed a file that does not decode")
	require.Contains(t, rec.msg, "decode "+bad)
}

func TestWriteFileFailsTheTestWhenItCannotWrite(t *testing.T) {
	t.Parallel()
	blocker := filepath.Join(t.TempDir(), "a-file")
	testkit.WriteFile(t, blocker, "x")
	rec := &recorder{TB: t}
	runs(rec, func() { testkit.WriteFile(rec, filepath.Join(blocker, "under", "it"), "y") })
	assert.True(t, rec.failed, "WriteFile passed a path whose parent is a file")
}

// recorder is the test's own testing.TB with the failure calls replaced: it
// records a failure instead of failing the test, so a helper's failure path
// can be asserted on.
type recorder struct {
	testing.TB
	failed, skipped bool
	msg             string
}

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.failed = true
	r.msg += fmt.Sprintf(format, args...)
}
func (r *recorder) FailNow() { panic(r) }
func (r *recorder) Skipf(format string, args ...any) {
	r.skipped = true
	r.msg += fmt.Sprintf(format, args...)
	panic(r)
}

// runs calls f, stopping at the recorder's FailNow as a test would stop.
func runs(r *recorder, f func()) {
	defer func() {
		if p := recover(); p != nil && p != any(r) {
			panic(p)
		}
	}()
	f()
}

type capture []byte

func (c *capture) Write(p []byte) (int, error) { *c = append(*c, p...); return len(p), nil }
