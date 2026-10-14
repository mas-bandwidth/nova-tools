package verb

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// recording is a testing.TB that records every failure instead of ending the
// test, so a failing row is observed, not assumed. It embeds the interface for
// the methods that stay unused here; every failure path is overridden.
type recording struct {
	testing.TB
	failed  bool
	message []string
}

func (r *recording) Helper()      {}
func (r *recording) Name() string { return "recording" }
func (r *recording) Fail()        { r.failed = true }
func (r *recording) FailNow()     { r.failed = true }
func (r *recording) Failed() bool { return r.failed }
func (r *recording) Error(args ...any) {
	r.failed = true
	r.message = append(r.message, fmt.Sprint(args...))
}
func (r *recording) Errorf(format string, args ...any) {
	r.failed = true
	r.message = append(r.message, fmt.Sprintf(format, args...))
}
func (r *recording) Fatal(args ...any) {
	r.failed = true
	r.message = append(r.message, fmt.Sprint(args...))
}
func (r *recording) Fatalf(format string, args ...any) {
	r.failed = true
	r.message = append(r.message, fmt.Sprintf(format, args...))
}

// The run the chain table holds its links against: a tool that refuses on
// stderr, prints its JSON and a line on stdout, and exits 2.
const (
	tableStdout = "{\"kind\":\"inspect\"}\n"
	tableStderr = "INSPECT REFUSED: no door given; run: nova-tool help inspect\n"
	tableCode   = 2
)

// tableTool is that run as a Main.
func tableTool() Main {
	return func(args []string, _ io.Reader, stdout, stderr io.Writer) int {
		fmt.Fprint(stdout, tableStdout)
		fmt.Fprint(stderr, tableStderr)
		return tableCode
	}
}

// TestChainLinksPassAndFail pins every link on a passing and a failing row: a
// row the run answers records no failure, a row it misses records one, and that
// failure prints both captured streams whole, so a reader sees the run without
// running it.
func TestChainLinksPassAndFail(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name    string
		link    func(Ran) Ran
		failing bool
	}{
		{"code-answers", func(r Ran) Ran { return r.Code(tableCode) }, false},
		{"code-misses", func(r Ran) Ran { return r.Code(0) }, true},
		{"stdout-answers", func(r Ran) Ran { return r.Stdout("inspect", "kind") }, false},
		{"stdout-misses", func(r Ran) Ran { return r.Stdout("nothing here") }, true},
		{"notstdout-answers", func(r Ran) Ran { return r.NotStdout("panic", "FAILED") }, false},
		{"notstdout-misses", func(r Ran) Ran { return r.NotStdout("inspect") }, true},
		{"stderr-answers", func(r Ran) Ran { return r.Stderr("REFUSED", "no door given") }, false},
		{"stderr-misses", func(r Ran) Ran { return r.Stderr("nothing here") }, true},
		{"notstderr-answers", func(r Ran) Ran { return r.NotStderr("panic") }, false},
		{"notstderr-misses", func(r Ran) Ran { return r.NotStderr("REFUSED") }, true},
		{"json-answers", func(r Ran) Ran {
			var into struct {
				Kind string `json:"kind"`
			}
			return r.JSON(&into)
		}, false},
		{"json-misses", func(r Ran) Ran {
			var into []string
			return r.JSON(&into)
		}, true},
		{"lines-answer", func(r Ran) Ran { return r.Lines(`{"kind":"inspect"}`) }, false},
		{"lines-miss", func(r Ran) Ran { return r.Lines("INSPECT OK kind=gadget") }, true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			rec := &recording{TB: t}
			row.link(Run(rec, tableTool(), "inspect", "--json"))
			if row.failing {
				assert.True(t, rec.failed, "a link the run misses recorded no failure")
				assert.NotEmpty(t, rec.message)
				joined := strings.Join(rec.message, "\n")
				assert.Contains(t, joined, tableStdout, "the failure prints stdout whole")
				assert.Contains(t, joined, tableStderr, "the failure prints stderr whole")
				return
			}
			assert.False(t, rec.failed, "a link the run answers recorded a failure: %v", rec.message)
		})
	}
}

// TestRunHoldsTheCapture pins Run itself: the Ran is pkg/testkit's capture of
// the arguments, the exit code and both streams, and it prints them as the
// failure message.
func TestRunHoldsTheCapture(t *testing.T) {
	t.Parallel()
	r := Run(t, tableTool(), "inspect", "--json")
	assert.Equal(t, []string{"inspect", "--json"}, r.Args)
	assert.Equal(t, tableCode, r.Result.Code)
	assert.Equal(t, tableStdout, r.Result.Stdout)
	assert.Equal(t, tableStderr, r.Result.Stderr)
	assert.Contains(t, r.String(), tableStdout)
	assert.Contains(t, r.String(), tableStderr)
	assert.Contains(t, r.String(), `run ["inspect" "--json"]`)
}

// judgeTool is a tool the refusal rows run: `send` refuses on stderr at exit 2
// and `list` answers in silence at exit 0.
func judgeTool(seen *sync.Mutex, ran *[]string) Main {
	return func(args []string, _ io.Reader, _, stderr io.Writer) int {
		seen.Lock()
		*ran = append(*ran, args[0])
		seen.Unlock()
		if args[0] == "send" {
			fmt.Fprint(stderr, "SEND REFUSED: --to is required; run: nova-tool help send\n")
			return 2
		}
		return 0
	}
}

// TestRefusalRowsPassAndFail pins the row's checks: a row whose code and stderr
// answer records no failure, and a row that misses its code, its stderr text or
// its wanted silence records one.
func TestRefusalRowsPassAndFail(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name    string
		row     Refusal
		failing bool
	}{
		{"the refusal answers", Refusal{Name: "no to", Args: []string{"send"}, Code: 2, Stderr: "--to is required"}, false},
		{"the run that answers is silent", Refusal{Name: "list", Args: []string{"list"}, Code: 0}, false},
		{"the code misses", Refusal{Name: "code", Args: []string{"send"}, Code: 0, Stderr: "--to is required"}, true},
		{"the stderr text misses", Refusal{Name: "text", Args: []string{"send"}, Code: 2, Stderr: "--from is required"}, true},
		{"a refusal where silence was wanted", Refusal{Name: "silence", Args: []string{"send"}, Code: 2}, true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var ran []string
			rec := &recording{TB: t}
			check(rec, judgeTool(&mu, &ran), row.row)
			assert.Equal(t, []string{row.row.Args[0]}, ran, "the row ran its own arguments once")
			if row.failing {
				assert.True(t, rec.failed, "a row that misses recorded no failure")
				assert.Contains(t, strings.Join(rec.message, "\n"), "SEND REFUSED: --to is required", "the failure prints stderr whole")
				return
			}
			assert.False(t, rec.failed, "a row that answers recorded a failure: %v", rec.message)
		})
	}
}

// TestRefusalsRunsEveryRowInParallel pins the table runner: every row runs, once,
// under its own name, and the rows run at the same time. The proof is a barrier
// in the tool: each run waits until every row has started, which a table that
// ran its rows one after another never releases.
func TestRefusalsRunsEveryRowInParallel(t *testing.T) {
	t.Parallel()
	rows := []Refusal{
		{Name: "the first refusal", Args: []string{"send"}, Code: 2, Stderr: "REFUSED"},
		{Name: "the run that answers", Args: []string{"list"}, Code: 0},
		{Name: "the second refusal", Args: []string{"send"}, Code: 2, Stderr: "--to is required"},
	}
	var started sync.WaitGroup
	started.Add(len(rows))
	var mu sync.Mutex
	var ran []string
	tool := judgeTool(&mu, &ran)
	// The parallel rows are resumed after this body returns, so the rows are
	// counted in a cleanup, which runs after every one of them has.
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		assert.ElementsMatch(t, []string{"send", "list", "send"}, ran, "every row ran, once")
	})
	Refusals(t, func(args []string, in io.Reader, stdout, stderr io.Writer) int {
		started.Done()
		started.Wait()
		return tool(args, in, stdout, stderr)
	}, rows)
}

// ExampleRun is the package doc's chain, run by the go tool: the tool answers,
// so every link passes and the recording TB records nothing.
func ExampleRun() {
	rec := &recording{}
	inspect := Main(func(args []string, _ io.Reader, stdout, _ io.Writer) int {
		fmt.Fprint(stdout, "{\"kind\":\"inspect\"}\n")
		return 0
	})
	var into struct {
		Kind string `json:"kind"`
	}
	Run(rec, inspect, "inspect", "--json").
		Code(0).
		Stdout("inspect").
		NotStderr("panic").
		JSON(&into).
		Lines(`{"kind":"inspect"}`)
	fmt.Println(into.Kind, rec.failed)
	// Output: inspect false
}
