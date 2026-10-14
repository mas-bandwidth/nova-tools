// Package verb runs a tool's entry point inside a test and checks the run as
// one fluent chain, and runs a tool's refusal table as parallel subtests:
//
//	verb.Run(t, main, "inspect", "--json").Code(0).Stdout("ok").NotStderr("panic")
//	verb.Refusals(t, main, []verb.Refusal{{Name: "no door", Args: nil, Code: 2, Stderr: "REFUSED"}})
//
// Every link is a testify require whose message is the run, so a failure prints
// the arguments, the exit code and both captured streams whole, and the chain
// stops at the first link that misses.
//
// It is a thin front door over pkg/testkit, which holds the capture and the
// chain: testkit.Main runs the entry point in process with both streams in
// buffers, and testkit.Ran already chains Exit, Out, NotOut, Err, NotErr and
// Refused over that Result. Nothing here captures a stream and nothing here
// writes an assert testify has. What verb adds is the names a tool test writes
// for those links (Code, Stdout, NotStdout, Stderr, NotStderr), a link that
// decodes stdout as the result's JSON, one that holds stdout to its lines, and
// a refusal table whose rows are parallel subtests, so a row that misses hides
// none of the rest. A run needing stdin is testkit.Main.DoIn, and a check verb
// has no link for is a testify call with the Ran as its message:
//
//	r := verb.Run(t, main, "inspect")
//	assert.Empty(t, r.Result.Stdout, r)
package verb

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/require"
)

// Main is a tool's entry point: the arguments after the tool's name, stdin, the
// two output streams, and the exit code it returns. It is pkg/testkit.Main,
// named here so a tool's tests import verb alone.
type Main = testkit.Main

// Ran is one run of a Main inside a test, with the checks as its links. Result
// is pkg/testkit's capture and Args the arguments run; every link returns the
// Ran, so the links chain.
type Ran struct {
	Result testkit.Result
	Args   []string
	t      testing.TB
}

// Run runs the entry point with args and an empty stdin. It checks nothing by
// itself: the checks are the Ran's links.
func Run(t testing.TB, m Main, args ...string) Ran {
	t.Helper()
	return Ran{Result: m.Run(args...), Args: args, t: t}
}

// String is the run as a failure prints it: the arguments, the exit code, and
// both streams whole, so a reader sees what the tool did without running it.
func (r Ran) String() string {
	return fmt.Sprintf("run %q: exit=%d\nstdout:\n%s\nstderr:\n%s", r.Args, r.Result.Code, r.Result.Stdout, r.Result.Stderr)
}

// says is one link's failure message: what the link wanted, then the run.
func (r Ran) says(want string) string { return want + "\n" + r.String() }

// Code is require.Equal on the exit code.
func (r Ran) Code(want int) Ran {
	r.t.Helper()
	require.Equal(r.t, want, r.Result.Code, r.says(fmt.Sprintf("exit %d wanted", want)))
	return r
}

// Stdout is require.Contains on stdout, for each of wants.
func (r Ran) Stdout(wants ...string) Ran {
	r.t.Helper()
	for _, w := range wants {
		require.Contains(r.t, r.Result.Stdout, w, r.says(fmt.Sprintf("stdout carries %q", w)))
	}
	return r
}

// NotStdout is require.NotContains on stdout, for each of nots.
func (r Ran) NotStdout(nots ...string) Ran {
	r.t.Helper()
	for _, n := range nots {
		require.NotContains(r.t, r.Result.Stdout, n, r.says(fmt.Sprintf("stdout carries none of %q", n)))
	}
	return r
}

// Stderr is require.Contains on stderr, for each of wants.
func (r Ran) Stderr(wants ...string) Ran {
	r.t.Helper()
	for _, w := range wants {
		require.Contains(r.t, r.Result.Stderr, w, r.says(fmt.Sprintf("stderr carries %q", w)))
	}
	return r
}

// NotStderr is require.NotContains on stderr, for each of nots.
func (r Ran) NotStderr(nots ...string) Ran {
	r.t.Helper()
	for _, n := range nots {
		require.NotContains(r.t, r.Result.Stderr, n, r.says(fmt.Sprintf("stderr carries none of %q", n)))
	}
	return r
}

// JSON is require.NoError on decoding stdout into into, so a verb's --json
// rendering is one link: .JSON(&result).
func (r Ran) JSON(into any) Ran {
	r.t.Helper()
	require.NoError(r.t, json.Unmarshal([]byte(r.Result.Stdout), into), r.says("stdout decodes as the result's JSON"))
	return r
}

// Lines is require.Equal on stdout's lines, a trailing newline dropped and an
// empty stdout no lines at all, so a listing's shape is one link.
func (r Ran) Lines(want ...string) Ran {
	r.t.Helper()
	var got []string
	if r.Result.Stdout != "" {
		got = strings.Split(strings.TrimSuffix(r.Result.Stdout, "\n"), "\n")
	}
	require.Equal(r.t, want, got, r.says("stdout is these lines"))
	return r
}

// Refusal is one row of a tool's refusal table: the subtest's name, the
// arguments to run, the exit code wanted, and what stderr must say. A row that
// leaves Stderr empty wants silence, nothing on stderr at all, which is what a
// run that answers looks like.
type Refusal struct {
	Name   string
	Args   []string
	Code   int
	Stderr string
}

// Refusals runs each row as its own subtest and checks it as Row does. The
// rows are parallel (docs/STANDARD.md section 8: every test opens with
// t.Parallel), so a row that misses hides none of the rows after it, and a row
// that needs the state another row leaves behind is not a row for this table.
func Refusals(t *testing.T, m Main, rows []Refusal) {
	t.Helper()
	require.NotEmpty(t, rows, "no refusal rows: a table of none checks nothing")
	for _, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			t.Parallel()
			check(t, m, row)
		})
	}
}

// check is the body Refusals runs per row: the exit code, then stderr carrying
// row.Stderr, or stderr carrying nothing where the row wants silence.
func check(t testing.TB, m Main, row Refusal) {
	t.Helper()
	r := Run(t, m, row.Args...)
	r.Code(row.Code)
	if row.Stderr == "" {
		require.Empty(t, r.Result.Stderr, r.says("the run that answers writes nothing on stderr"))
		return
	}
	r.Stderr(row.Stderr)
}
