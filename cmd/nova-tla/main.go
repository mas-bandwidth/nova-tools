// Command nova-tla runs the TLA+ model checks of a repository: the declared cases
// of tla/CASES.tsv with their run records (groups, inputs, run, merge), and, for
// nova-tools' own table model, its suites, the replay of its findings against a
// table.lua and the execution replay of table.lua receipts against EpochMemberTable.
//
// It is a bench tool: TLC needs java, the replays need redis-server, and the runs
// are bounded (docs/SPEC-CI.md, the tlc job). Run it on a bench, never on a
// working machine, or reach a bench from here with run --bench. The reading of
// TLC's results and the suites live in pkg/tlc and internal/tablemodel; this
// package is the command line, on pkg/tool: the dispatch, the banner, the help,
// the flags and the refusal grammar are the skeleton's, and each verb that streams
// its event lines declares Prints and writes them itself (docs/STANDARD.md
// section 2).
//
// It is the one TLA+ runner, shipped in the release with every other command.
// nova-sprint runs it at the nova-tools version its go.mod names (go tool nova-tla,
// from a tool line). One program serves every repository, so which runner a tree's
// records are measured with is decided by the tree, never by who built the binary
// (runner.go, nova-tla help runner).
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// name is the command's name, the first word of every remedy it prints.
const name = "nova-tla"

// env is everything the command reads from outside itself, so the tests run it
// with none of it: no java, no redis-server, no environment, no clock. stdout and
// stderr are the streams of the verb's call, set when the verb runs.
type env struct {
	stdout, stderr io.Writer
	getenv         func(string) string
	lookPath       func(string) (string, error)
	cpus           func() int                        // logical CPUs of this machine
	javaVersion    func(java string) (string, error) // the version `java -version` reports
	now            func() time.Time                  // the clock a verb's seconds= are read on
	exec           tlc.Executor
	goos           string
	goarch         string
	tmpDir         string    // where a disposable store's directory goes; the system default when empty
	bench          benchDeps // run --bench: the bench machines, the fleet's rows, the build
	built          string    // the nova-tools this binary was built from (buildModule); the tests name their own
}

func main() {
	os.Exit(newTool(env{
		getenv: os.Getenv, lookPath: tlc.LookPath, cpus: runtime.NumCPU, javaVersion: javaVersion, now: time.Now,
		exec: tlc.Execute, goos: hostOS, goarch: hostArch, bench: realBench(), built: buildModule(),
	}).Main())
}

// javaVersion runs the java it is given with -version, bounded, and returns
// the version it reports. It runs java, so it belongs on a bench.
func javaVersion(java string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return tlc.ReadJavaVersion(ctx, java)
}

// verb is one verb's body: the env with the call's streams, and the call.
type verb func(e env, c *tool.Call) *tool.Out

// on binds a verb's body to e: the streams it prints its events on are the call's.
func (e env) on(do verb) func(c *tool.Call) *tool.Out {
	return func(c *tool.Call) *tool.Out {
		e.stdout, e.stderr = c.Stdout, c.Stderr
		return do(e, c)
	}
}

// newTool is nova-tla over e: its verbs, help and help topic (help.go).
func newTool(e env) *tool.Tool {
	return &tool.Tool{
		Name:  name,
		What:  "the TLA+ runner of every repository: TLC over a checkout's declared cases, their run records, and which records are stale",
		Stamp: version,
		How: `a case is a row of tla/CASES.tsv: a config, its module and the result TLC must reach.
run measures a group of cases in a private copy under --dir and writes one record each to RUNS.tsv;
tla/RUNS.tsv is the committed set, and a record is current while its case's fingerprint holds.
TLC needs java on Linux: run there, or run --bench <machine>|any from here, which merges records.`,
		ExitTable:     "0 passed (every case as declared), 1 it ran and said no (a case, the budget, the trough or the merge), 2 could not run (a flag, an input, or a tree whose runner this binary is not)",
		Topics:        []tool.Topic{{Name: "runner", Text: runnerTopic()}},
		VersionExtras: versionExtras,
		Verbs:         verbs(e),
	}
}

// refuse is a refusal naming its cause and the command to run next: exit 2,
// could not run.
func refuse(cause, next string) *tool.Out {
	o := tool.Refuse(cause)
	o.Remedy = next
	return o
}

// event prints one event line. Values are escaped as fields.
func event(w io.Writer, token, status string, kv ...string) {
	writeLine(w, line(token, status, kv...))
}

// eventWhy prints an event whose last part is a sentence: the key=value fields,
// then a colon and the reason, escaped so the line stays one line.
func eventWhy(w io.Writer, token, status, why string, kv ...string) {
	writeLine(w, line(token, status, kv...)+": "+oneline.Escape(why))
}

// line is `<TOKEN> <STATUS> k=v ...`, each value one field.
func line(token, status string, kv ...string) string {
	s := token + " " + status
	for i := 0; i+1 < len(kv); i += 2 {
		s += " " + kv[i] + "=" + oneline.Field(kv[i+1])
	}
	return s
}

// writeLine writes one line to a verb's stream. A stream that cannot be written
// is a closed pipe with no reader left to tell.
func writeLine(w io.Writer, s string) {
	_, _ = io.WriteString(w, s+"\n") // ignored: a closed stream has no reader to tell, and the verb's exit still says how it ended
}

func seconds(d time.Duration) string { return fmt.Sprintf("%.2f", d.Seconds()) }

func oneLine(s string) string { return oneline.Escape(s) }
