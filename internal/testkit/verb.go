package testkit

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Ran is one run of a Main made inside a test, with its checks as methods.
// Each check is the testify require call it names, with the Ran as its
// message, so a failing line prints the arguments run, the exit code and both
// streams; each returns the Ran, so checks chain:
//
//	tool.Do(t, "send").Exit(2).Err("--to is required").NotOut("OK")
//
// The fields are the Result's; a check the methods do not cover is a testify
// call with the Ran as its message: assert.Empty(t, r.Stdout, r).
type Ran struct {
	Result
	Args []string
	t    testing.TB
}

// Do runs the entry point with args and an empty stdin. It checks nothing by
// itself: the checks are the Ran's methods.
func (m Main) Do(t testing.TB, args ...string) Ran {
	t.Helper()
	return m.DoIn(t, "", args...)
}

// DoIn is Do with stdin holding the given text.
func (m Main) DoIn(t testing.TB, stdin string, args ...string) Ran {
	t.Helper()
	return Ran{Result: m.RunIn(stdin, args...), Args: args, t: t}
}

// String is the run as a failure prints it: the arguments, the exit code and
// both streams.
func (r Ran) String() string {
	return fmt.Sprintf("run %q: exit=%d stdout=%q stderr=%q", r.Args, r.Code, r.Stdout, r.Stderr)
}

// Exit is require.Equal on the exit code.
func (r Ran) Exit(want int) Ran {
	r.t.Helper()
	require.Equal(r.t, want, r.Code, r)
	return r
}

// Out is require.Contains on stdout, for each of wants.
func (r Ran) Out(wants ...string) Ran {
	r.t.Helper()
	for _, w := range wants {
		require.Contains(r.t, r.Stdout, w, r)
	}
	return r
}

// NotOut is require.NotContains on stdout, for each of nots.
func (r Ran) NotOut(nots ...string) Ran {
	r.t.Helper()
	for _, n := range nots {
		require.NotContains(r.t, r.Stdout, n, r)
	}
	return r
}

// Err is require.Contains on stderr, for each of wants.
func (r Ran) Err(wants ...string) Ran {
	r.t.Helper()
	for _, w := range wants {
		require.Contains(r.t, r.Stderr, w, r)
	}
	return r
}

// NotErr is require.NotContains on stderr, for each of nots.
func (r Ran) NotErr(nots ...string) Ran {
	r.t.Helper()
	for _, n := range nots {
		require.NotContains(r.t, r.Stderr, n, r)
	}
	return r
}

// The refusal grammar, `<TOKEN> REFUSED: <reason>; run: <remedy>`
// (docs/STANDARD.md, "The status word leads every line"): its status word and
// the mark of its remedy.
const (
	refusedWord = "REFUSED"
	remedyMark  = "; run: "
)

// Refused fails the test unless the run is a refusal in the house grammar
// that says says: a non-zero exit, and a stderr line holding says that also
// carries the status word REFUSED and a remedy (`; run: `). Every clause it
// misses is named at once.
func (r Ran) Refused(says string) Ran {
	r.t.Helper()
	if problems := r.refusal(says); len(problems) > 0 {
		require.FailNow(r.t, "not the refusal: "+strings.Join(problems, "; "), r.String())
	}
	return r
}

// refusal is every clause of Refused the run misses.
func (r Ran) refusal(says string) []string {
	var problems []string
	if r.Code == 0 {
		problems = append(problems, "exit 0: a refusal never exits 0")
	}
	i := strings.Index(r.Stderr, says)
	if i < 0 {
		return append(problems, fmt.Sprintf("no stderr line says %q", says))
	}
	start := strings.LastIndex(r.Stderr[:i], "\n") + 1
	line, _, _ := strings.Cut(r.Stderr[start:], "\n")
	if !strings.Contains(line, refusedWord) {
		problems = append(problems, fmt.Sprintf("the line saying %q has no status word %s", says, refusedWord))
	}
	if !strings.Contains(line, remedyMark) {
		problems = append(problems, fmt.Sprintf("the line saying %q has no remedy %q", says, remedyMark))
	}
	return problems
}

// Refusal is one row of a tool's refusal table: the arguments, the exit code
// wanted, and what the refusal line must say.
type Refusal struct {
	Args []string
	Code int
	Says string
}

// Refusals runs each row and checks it as Exit(row.Code) and Refused(row.Says)
// do, so a tool's refusal tests are one row each. A row that misses fails the
// test through assert, naming the run, and the rows after it still run. The
// rows run one after another: m may close over state its runs share.
func Refusals(t testing.TB, m Main, rows []Refusal) {
	t.Helper()
	require.NotEmpty(t, rows, "no refusal rows: a table of none checks nothing")
	for _, row := range rows {
		r := m.Do(t, row.Args...)
		problems := r.refusal(row.Says)
		if r.Code != row.Code {
			problems = append([]string{fmt.Sprintf("exit %d, want %d", r.Code, row.Code)}, problems...)
		}
		if len(problems) > 0 {
			assert.Fail(t, "not the refusal: "+strings.Join(problems, "; "), r.String())
		}
	}
}
