package tool

import (
	"os"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The unit cover of the two functions a reader found at 0.0% in tool.go:
// Tool.Main and Call.Dur. Main is the process's own door, so the test stands
// the process's arguments and streams up for the call and puts them back after;
// Dur is read through a verb that declares a duration flag, the way every tool
// declares one. Nothing here waits, dials or starts a child.

// durationTool is a tool whose one verb reads a duration flag with Call.Dur.
func durationTool() *Tool {
	return &Tool{
		Name:      "nova-cover",
		What:      "reads one duration",
		ExitTable: "0 done, 1 said no, 2 could not run.",
		Verbs: []Verb{
			{
				Name: "wait", Usage: "wait --for <dur>", Effect: Inspection,
				Flags: func(f *Flags) { f.Duration("for", 0, "how long to wait") },
				Run:   func(c *Call) *Out { return Done().Fact("for", c.Dur("for")) },
			},
		},
	}
}

// TestToolCoverDurReadsADurationFlag pins Call.Dur's main path: a duration flag
// is read back as a time.Duration and rendered as its own string.
func TestToolCoverDurReadsADurationFlag(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, given, want string }{
		{"a compound duration", "1m30s", "for=1m30s"},
		{"the zero value", "0s", "for=0s"},
		{"a sub-second duration", "250ms", "for=250ms"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := testkit.Main(durationTool().Run).Run("wait", "--for", tc.given)
			assert.Equal(t, 0, r.Code, "stdout=%q stderr=%q", r.Stdout, r.Stderr)
			assert.Contains(t, r.Stdout, tc.want)
		})
	}
}

// TestToolCoverDurRefusesABadDuration pins the refusal beside Dur's main path:
// a value the duration flag cannot parse is refused before the verb runs, with
// the flag named and what it wants.
func TestToolCoverDurRefusesABadDuration(t *testing.T) {
	t.Parallel()
	r := testkit.Main(durationTool().Run).Run("wait", "--for", "soon")
	assert.Equal(t, 2, r.Code)
	assert.Contains(t, r.Stderr, "invalid value for --for: it wants a duration")
	assert.Contains(t, r.Stderr, "run: nova-cover wait -h")
}

// TestToolCoverMainRunsAndRefuses pins Tool.Main: it hands the process's own
// arguments and streams to Run, so the test stands them up for the call and
// puts every one of them back when it returns.
func TestToolCoverMainRunsAndRefuses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		args        []string
		code        int
		stdout      string // substring, unless stdoutEmpty
		stdoutEmpty bool
		stderr      string // substring, unless stderrEmpty
		stderrEmpty bool
	}{
		{"main path: version runs at exit 0 on stdout", []string{"version"}, 0, "nova-demo v9.9.9 ", false, "", true},
		{"refusal: a bare call is exit 2 on stderr", nil, 2, "", true, "DEMO REFUSED: no verb given", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runMain(t, tc.args...)
			assert.Equal(t, tc.code, code, "stdout=%q stderr=%q", stdout, stderr)
			if tc.stdoutEmpty {
				assert.Empty(t, stdout)
			} else {
				assert.Contains(t, stdout, tc.stdout)
			}
			if tc.stderrEmpty {
				assert.Empty(t, stderr)
			} else {
				assert.Contains(t, stderr, tc.stderr)
			}
		})
	}
}

// runMain calls demo().Main with the process's own door stood up for the call:
// os.Args after the tool's name, both outputs to files in the test's temp dir.
// It puts every global back when the call returns.
func runMain(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "stdout")
	require.NoError(t, err)
	errf, err := os.CreateTemp(t.TempDir(), "stderr")
	require.NoError(t, err)
	origArgs, origOut, origErr := os.Args, os.Stdout, os.Stderr
	os.Args = append([]string{"nova-demo"}, args...)
	os.Stdout, os.Stderr = out, errf
	defer func() { os.Args, os.Stdout, os.Stderr = origArgs, origOut, origErr }()
	code = demo().Main()
	require.NoError(t, out.Close())
	require.NoError(t, errf.Close())
	rawOut, err := os.ReadFile(out.Name())
	require.NoError(t, err)
	rawErr, err := os.ReadFile(errf.Name())
	require.NoError(t, err)
	return code, string(rawOut), string(rawErr)
}
