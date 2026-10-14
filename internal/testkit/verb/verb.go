// Package verb is the verb harness every tool test needs: it runs a tool's
// entry point in process and checks the run as a fluent chain, and it runs a
// tool's refusal table one row per subtest. It is a thin front door over
// pkg/testkit, which already holds the capture (Main, Ran) and the same
// chain under its own names (Exit, Out, Err, Refused): this package imports
// that one and copies none of it, adding the names a tool's tests read
// (Code, Stdout, Stderr), the JSON and Lines links, and a refusal table
// whose rows run as their own parallel subtests (docs/STANDARD.md,
// section 8: shared rigs a test imports, never copies).
//
//	verb.Run(t, main, "inspect", "--json").Code(0).Stdout("ok").NotStderr("panic")
//	verb.Refusals(t, main, []verb.Refusal{{Name: "no door", Args: nil, Code: 2, Stderr: "REFUSED"}})
package verb

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/require"
)

// Ran is one run of a Main, made by Run, with its checks as chain links.
// It embeds pkg/testkit's Ran, so that kit's own links (Exit, Out, Err,
// Refused) stay reachable, and the run's values are that Ran's fields
// (r.Ran.Code, r.Ran.Stdout, r.Ran.Stderr): the link names below take the
// words the fields had. Every link prints the captured run — the
// arguments, the exit code and both streams (testkit.Ran.String) — when it
// fails.
type Ran struct {
	testkit.Ran
	t testing.TB
}

// Run calls the entry point with args and an empty stdin, capturing both
// streams (pkg/testkit.Main.Do); the checks are the Ran's chain links.
func Run(t testing.TB, m testkit.Main, args ...string) Ran {
	t.Helper()
	return Ran{Ran: m.Do(t, args...), t: t}
}

// Code is require.Equal on the exit code.
func (r Ran) Code(want int) Ran {
	r.Ran.Exit(want)
	return r
}

// Stdout is require.Contains on stdout, for each of wants.
func (r Ran) Stdout(wants ...string) Ran {
	r.Ran.Out(wants...)
	return r
}

// NotStdout is require.NotContains on stdout, for each of nots.
func (r Ran) NotStdout(nots ...string) Ran {
	r.Ran.NotOut(nots...)
	return r
}

// Stderr is require.Contains on stderr, for each of wants.
func (r Ran) Stderr(wants ...string) Ran {
	r.Ran.Err(wants...)
	return r
}

// NotStderr is require.NotContains on stderr, for each of nots.
func (r Ran) NotStderr(nots ...string) Ran {
	r.Ran.NotErr(nots...)
	return r
}

// JSON decodes stdout into into and fails the test when it does not decode.
func (r Ran) JSON(into any) Ran {
	r.t.Helper()
	require.NoError(r.t, json.Unmarshal([]byte(r.Ran.Stdout), into), r)
	return r
}

// Lines is require.Equal on stdout's lines: each complete newline-terminated
// line one entry, no trailing empty entry, and empty stdout no entries.
func (r Ran) Lines(want ...string) Ran {
	r.t.Helper()
	require.Equal(r.t, want, lines(r.Ran.Stdout), r)
	return r
}

// lines is stdout as its complete lines, the trailing newline dropped and
// empty stdout no lines.
func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// Refusal is one row of a tool's refusal table: the row's name, the
// arguments, the exit code wanted, and the text the refusal line must say
// on stderr.
type Refusal struct {
	Name   string
	Args   []string
	Code   int
	Stderr string
}

// Refusals runs each row as its own t.Run with t.Parallel
// (docs/STANDARD.md, section 8), checking the run as
// Run(...).Code(row.Code).Stderr(row.Stderr) does: the exit code, and the
// refusal line's text on stderr.
func Refusals(t *testing.T, m testkit.Main, rows []Refusal) {
	t.Helper()
	requireRows(t, rows)
	for _, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			t.Parallel()
			Run(t, m, row.Args...).Code(row.Code).Stderr(row.Stderr)
		})
	}
}

// requireRows refuses a table of no rows: a table of none checks nothing.
func requireRows(t testing.TB, rows []Refusal) {
	t.Helper()
	require.NotEmpty(t, rows, "no refusal rows: a table of none checks nothing")
}
