package verb

import (
	"fmt"
	"io"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
)

// tells prints fixed streams and exits 2: a run whose code and streams the
// link rows bend.
var tells = testkit.Main(func(_ []string, _ io.Reader, stdout, stderr io.Writer) int {
	fmt.Fprint(stdout, "out-text")
	fmt.Fprint(stderr, "err-text")
	return 2
})

// jsonish prints its one JSON fact: a run the JSON link decodes.
var jsonish = testkit.Main(func(_ []string, _ io.Reader, stdout, _ io.Writer) int {
	fmt.Fprint(stdout, `{"name": "nova"}`)
	return 0
})

// recorder is the test's own testing.TB with the failure calls replaced: it
// records a failure instead of failing the test, so a link's failure path
// can be asserted on.
type recorder struct {
	testing.TB
	failed bool
	msg    string
}

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.failed = true
	r.msg += fmt.Sprintf(format, args...)
}
func (r *recorder) FailNow() { panic(r) }

// runs calls f, stopping at the recorder's FailNow as a test would stop.
func runs(r *recorder, f func()) {
	defer func() {
		if p := recover(); p != nil && p != any(r) {
			panic(p)
		}
	}()
	f()
}

// TestVerbChainFailsOnEachLink gives every link a passing and a failing
// row: the passing row on a recording TB holds, the failing one goes red
// naming what the link checks and both captured streams (pkg/testkit.Ran's
// message), so a failure can be read without running the tool again.
func TestVerbChainFailsOnEachLink(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		pass, fail func(t testing.TB)
		want       string
	}{
		{"Code",
			func(t testing.TB) { Run(t, tells).Code(2) },
			func(t testing.TB) { Run(t, tells).Code(0) },
			`exit=2`},
		{"Stdout",
			func(t testing.TB) { Run(t, tells).Stdout("out-text") },
			func(t testing.TB) { Run(t, tells).Stdout("nowhere") },
			`stdout="out-text"`},
		{"NotStdout",
			func(t testing.TB) { Run(t, tells).NotStdout("nowhere") },
			func(t testing.TB) { Run(t, tells).NotStdout("out-text") },
			"out-text"},
		{"Stderr",
			func(t testing.TB) { Run(t, tells).Stderr("err-text") },
			func(t testing.TB) { Run(t, tells).Stderr("nowhere") },
			`stderr="err-text"`},
		{"NotStderr",
			func(t testing.TB) { Run(t, tells).NotStderr("nowhere") },
			func(t testing.TB) { Run(t, tells).NotStderr("err-text") },
			"err-text"},
		{"JSON",
			func(t testing.TB) {
				var got struct{ Name string }
				Run(t, jsonish).JSON(&got)
				assert.Equal(t, "nova", got.Name)
			},
			func(t testing.TB) { Run(t, tells).JSON(&struct{ Name string }{}) },
			"invalid character"},
		{"Lines",
			func(t testing.TB) { Run(t, tells).Lines("out-text") },
			func(t testing.TB) { Run(t, tells).Lines("other") },
			"out-text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{TB: t}
			runs(rec, func() { tc.pass(rec) })
			assert.False(t, rec.failed, "the link failed a run that holds it: %s", rec.msg)

			rec = &recorder{TB: t}
			runs(rec, func() { tc.fail(rec) })
			assert.True(t, rec.failed, "the link passed a run that breaks it")
			assert.Contains(t, rec.msg, tc.want, "the failure does not name what the link checks")
			assert.Contains(t, rec.msg, `stdout="out-text"`, "the failure does not print the captured stdout")
			assert.Contains(t, rec.msg, `stderr="err-text"`, "the failure does not print the captured stderr")
		})
	}
}

// TestRefusalsRunsEveryRowInParallel pins the table runner's shape: every
// row runs in its own subtest marked t.Parallel, so all rows are inside
// their run at once. Each row's Main waits — a Gosched spin, no clock —
// until every row has entered; a runner that forgot t.Parallel, or dropped
// a row, leaves the rows alone and their own checks go red.
func TestRefusalsRunsEveryRowInParallel(t *testing.T) {
	t.Parallel()
	const n = 4
	var inside atomic.Int32
	together := testkit.Main(func(_ []string, _ io.Reader, _, stderr io.Writer) int {
		inside.Add(1)
		for i := 0; inside.Load() < n && i < 1000000; i++ {
			runtime.Gosched()
		}
		if inside.Load() < n {
			fmt.Fprint(stderr, "alone")
			return 1
		}
		fmt.Fprint(stderr, "together")
		return 0
	})
	rows := make([]Refusal, n)
	for i := range rows {
		rows[i] = Refusal{Name: "row " + strconv.Itoa(i), Code: 0, Stderr: "together"}
	}
	Refusals(t, together, rows)
}

// TestRefusalsRefusesATableOfNoRows pins the runner's own refusal: a table
// of no rows checks nothing and says so.
func TestRefusalsRefusesATableOfNoRows(t *testing.T) {
	t.Parallel()
	rec := &recorder{TB: t}
	runs(rec, func() { requireRows(rec, nil) })
	assert.True(t, rec.failed, "a table of no rows was passed")
	assert.Contains(t, rec.msg, "a table of none checks nothing")
}

// ExampleRun is the chain as a tool's test writes it: every link a require
// that prints the captured run when it fails, nothing at all when it holds.
func ExampleRun() {
	hello := testkit.Main(func(args []string, _ io.Reader, stdout, _ io.Writer) int {
		fmt.Fprintln(stdout, "HELLO OK name="+args[0])
		return 0
	})
	Run(quietTB{}, hello, "reader").Code(0).Stdout("HELLO OK name=reader").NotStderr("REFUSED")
	// Output:
}

// quietTB is the smallest testing.TB a passing chain needs: the Example has
// no test to fail, and a passing chain calls nothing but Helper.
type quietTB struct{ testing.TB }

func (quietTB) Helper() {}
