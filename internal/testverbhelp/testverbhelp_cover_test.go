package testverbhelp

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeToolRun is the tool the coverage tests hand the seam: help prints the
// verb's usage on stdout at exit 0 and writes nothing, UnknownFlag is refused
// at exit 2 with the flag named and a remedy on stderr, and an invocation that
// is neither is refused the same way. It meets the rule, so every check run
// over it must find no problems.
func fakeToolRun(args []string, stdout, stderr io.Writer) int {
	switch last := args[len(args)-1]; {
	case last == "-h", last == "--help":
		fmt.Fprintf(stdout, "usage: nova-demo %s [flags]\n", strings.Join(args[:len(args)-1], " "))
		return 0
	case last == UnknownFlag:
		fmt.Fprintf(stderr, "nova-demo %s REFUSED: flag not defined: %s; run: nova-demo help\n", strings.Join(args[:len(args)-1], " "), UnknownFlag)
		return 2
	case args[0] == "help":
		fmt.Fprintf(stdout, "usage: nova-demo %s [flags]\n", strings.Join(args[1:], " "))
		return 0
	default:
		fmt.Fprintf(stderr, "nova-demo REFUSED: unknown invocation: %s; run: nova-demo help\n", strings.Join(args, " "))
		return 2
	}
}

// countRun wraps a Run and counts its calls, so a test pins that the check
// really handed every case, spelling and refusal to the tool.
func countRun(run Run, calls *atomic.Int32) Run {
	return func(args []string, stdout, stderr io.Writer) int {
		calls.Add(1)
		return run(args, stdout, stderr)
	}
}

// TestTestverbhelpCoverCheckRunsEveryCase pins Check's wiring: every case runs
// through One with -h and with --help, and through RefusalProblems with
// UnknownFlag, once each. A tool that meets the rule keeps every subtest quiet;
// the call count catches a Check that dropped one of the three legs.
func TestTestverbhelpCoverCheckRunsEveryCase(t *testing.T) {
	t.Parallel()
	cases := []Case{
		{Verb: "send", Flags: []string{"--dir", "{dir}", "--addr", "{addr}"}},
		{Verb: "slots take", Flags: []string{"--dir", "{dir}"}},
	}
	var calls atomic.Int32
	t.Cleanup(func() {
		assert.Equal(t, int32(len(cases)*3), calls.Load(), "every case runs -h, --help and the unknown flag")
	})
	Check(t, countRun(fakeToolRun, &calls), cases)
}

// TestTestverbhelpCoverRefusalProblemsNamesEveryBrokenRefusal pins the four
// refusal clauses of RefusalProblems, one broken tool per case, and the clean
// refusal that finds no problem. Each row names the mutation it catches.
func TestTestverbhelpCoverRefusalProblemsNamesEveryBrokenRefusal(t *testing.T) {
	t.Parallel()
	c := Case{Verb: "send", Flags: []string{"--dir", "{dir}", "--addr", "{addr}"}}
	for _, row := range []struct {
		name string
		run  Run
		want string
	}{
		{"silent refusal", func(args []string, stdout, stderr io.Writer) int {
			return 2
		}, "said nothing on stderr"},
		{"refusal that accepts", func(args []string, stdout, stderr io.Writer) int {
			fmt.Fprintln(stderr, "nova-demo send REFUSED: flag not defined; run: nova-demo help")
			return 0
		}, "exited 0 on a flag it does not define"},
		{"refusal with no breadcrumb", func(args []string, stdout, stderr io.Writer) int {
			fmt.Fprintln(stderr, "something bad happened")
			return 2
		}, "refused with no breadcrumb"},
		{"refusal closing with OK", func(args []string, stdout, stderr io.Writer) int {
			fmt.Fprintln(stderr, "nova-demo send REFUSED: flag not defined; run: nova-demo help")
			fmt.Fprintln(stdout, "SEND OK")
			return 2
		}, "says OK"},
		{"refusal that wrote", func(args []string, stdout, stderr io.Writer) int {
			_ = os.WriteFile(filepath.Join(flagValue(args, "dir"), "state"), nil, 0o600)
			fmt.Fprintln(stderr, "nova-demo send REFUSED: flag not defined; run: nova-demo help")
			return 2
		}, "left entries under its temp dir"},
	} {
		row := row
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			assert.Contains(t, strings.Join(RefusalProblems(row.run, c, t.TempDir()), "\n"), row.want)
		})
	}
	t.Run("clean refusal", func(t *testing.T) {
		t.Parallel()
		var got []string
		record := func(args []string, stdout, stderr io.Writer) int {
			got = args
			return fakeToolRun(args, stdout, stderr)
		}
		dir := t.TempDir()
		assert.Empty(t, RefusalProblems(record, c, dir), "a refusal that meets the rule")
		require.Len(t, got, 6)
		assert.Equal(t, []string{"send", "--dir", dir, "--addr", RefusedAddr, UnknownFlag}, got,
			"{dir} and {addr} become the case's temp dir and the refused address")
	})
}

// TestTestverbhelpCoverBreadcrumbAcceptsFlagUsageOrRemedy pins the three ways
// a refusal points somewhere, and the silence that does not.
func TestTestverbhelpCoverBreadcrumbAcceptsFlagUsageOrRemedy(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name string
		said string
		want bool
	}{
		{"names the flag", fmt.Sprintf("flag not defined: %s", UnknownFlag), true},
		{"prints the usage", "Usage: nova-demo send [flags]", true},
		{"carries a house remedy", "send REFUSED: no store; run: nova-sprint where", true},
		{"says nothing useful", "something bad happened", false},
	} {
		row := row
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, row.want, breadcrumb(row.said))
		})
	}
}

// TestTestverbhelpCoverOkClosingReadsTheLastLineOnly pins that a run closes
// with OK exactly when its last non-empty line is an OK event line: words
// after, fields with a value, and lowercase are not OK closings.
func TestTestverbhelpCoverOkClosingReadsTheLastLineOnly(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name string
		out  string
		want bool
	}{
		{"OK closes", "SEND OK\n", true},
		{"OK on the last line", "NOTE working\nSEND OK\n", true},
		{"OK earlier is not a close", "SEND OK\nNOTE still going\n", false},
		{"a failure word last", "NOTE all done\nSEND FAILED: nope\n", false},
		{"lowercase ok is a field", "SEND ok\n", false},
		{"a word with = is a field", "SEND DONE CODE=OK\n", false},
		{"no output", "", false},
	} {
		row := row
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, row.want, okClosing(row.out))
		})
	}
}

// TestTestverbhelpCoverOneRunsTheSeamWithNoProblems pins One's main path: it
// hands the case, with {dir} and {addr} replaced and the spelling appended, to
// the tool exactly once, and a tool that meets the rule keeps t quiet. The
// ways a run can break the rule are the returned problems, pinned by
// TestProblemsCatchesEveryWayToBreakTheRule.
func TestTestverbhelpCoverOneRunsTheSeamWithNoProblems(t *testing.T) {
	t.Parallel()
	c := Case{Verb: "send", Flags: []string{"--dir", "{dir}", "--addr", "{addr}"}}
	var calls atomic.Int32
	run := countRun(fakeToolRun, &calls)
	One(t, run, c, "-h")
	One(t, run, c, "--help")
	assert.Equal(t, int32(2), calls.Load(), "One handed the case and each spelling to the tool once")
}

// TestTestverbhelpCoverHelpVerbMatchesTheFlagSpelling pins HelpVerb's checks:
// `help <verb>` exits 0 with nothing on stderr, opens with the tool's usage
// line for that verb, and prints exactly what `<verb> -h` prints. A mismatch
// is a t.Error, which fails this test; the call count pins both spellings run.
func TestTestverbhelpCoverHelpVerbMatchesTheFlagSpelling(t *testing.T) {
	t.Parallel()
	verbs := []string{"send", "slots take"}
	var calls atomic.Int32
	t.Cleanup(func() {
		assert.Equal(t, int32(len(verbs)*2), calls.Load(), "each verb runs through help and through -h")
	})
	HelpVerb(t, countRun(fakeToolRun, &calls), "nova-demo", verbs...)
}
