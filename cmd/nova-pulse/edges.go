package main

// THIS TOOL FILES ITS OWN EDGES (class J, #828). Three refusal points, one row each:
//
//	an unknown or unparseable flag   the caller reached for something that is not there
//	a missing required input         the caller could not tell what the verb wanted
//	output over its own bound        the verb answered with more than a reader can spend
//
// Each appends one row to <queue>/EDGES.tsv, deduplicated by (tool, verb, line), and
// `nova-check edges` turns the distinct rows into dogfood issues. Recording never changes
// what the verb does or what it prints: the refusal is the answer, and the row is a note
// about this tool made beside it.
//
// The queue comes off the invocation's own --queue, or NOVA_QUEUE for the verbs that take
// no queue flag. An invocation that names neither files nothing -- a tool run outside a
// queue has nowhere to file, and inventing a directory to write into would be a worse bug
// than the one being recorded.

import (
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/edges"
)

// edgeTool is the name every row this binary writes carries.
const edgeTool = "nova-pulse"

// edgeQueue is the queue this invocation belongs to: its own --queue, else $NOVA_QUEUE,
// else nowhere.
func edgeQueue(args []string) string {
	for i, a := range args {
		if v, ok := strings.CutPrefix(a, "--queue="); ok {
			return v
		}
		if v, ok := strings.CutPrefix(a, "-queue="); ok {
			return v
		}
		if (a == "--queue" || a == "-queue") && i+1 < len(args) {
			return args[i+1]
		}
	}
	return strings.TrimSpace(os.Getenv("NOVA_QUEUE"))
}

// recordEdge files one row, best effort. A failure to file is swallowed on purpose: the
// caller is in the middle of making a refusal, and a note about the tool is never allowed to
// take the place of the tool's own answer.
func recordEdge(queue, verb, line, expected string) {
	_ = edges.Record(queue, edgeTool, verb, line, expected)
}

// The three expectations, written once so two refusal points never word the same edge two
// ways and file it twice.
const (
	expectFlag = "the flag to exist, or the refusal to name the flag that replaced it"
	expectWant = "the verb to say what the flag wants in the same line that refuses the run, so the next invocation is right"
	expectCap  = "the verb's output to fit its own bound without a MORE line, or the bound to be the right size for this queue"
)

// moreMark is how every bounded listing in this estate says it printed less than it counted
// (internal/bounded): `<TOKEN> MORE kind=<k> shown=<n> total=<m> <remedy>`. A verb that
// prints one has answered over its own bound, which is the third refusal point.
const moreMark = " MORE kind="

// edgeWatcher passes a stream through untouched and files ONE row the first time a bounded
// listing says it elided something. It is a watcher and not a filter: every byte reaches the
// caller's stream in the order it was written, and the row is a note made beside it.
//
// Only the FIRST such line is filed per run. A verb whose output is over its bound in three
// places has one edge -- the bound is too small for this queue -- and three rows would be
// three issues about one thing.
type edgeWatcher struct {
	w     io.Writer
	queue string
	verb  string
	filed bool
	tail  []byte
}

func (e *edgeWatcher) Write(p []byte) (int, error) {
	n, err := e.w.Write(p)
	if e.filed || e.queue == "" {
		return n, err
	}
	// The mark can straddle two writes, so a short tail is kept: long enough to hold the
	// mark itself with a token in front of it, short enough to cost nothing.
	e.tail = append(e.tail, p[:n]...)
	if len(e.tail) > 4096 {
		e.tail = e.tail[len(e.tail)-4096:]
	}
	for _, line := range strings.Split(string(e.tail), "\n") {
		if !strings.Contains(line, moreMark) {
			continue
		}
		e.filed = true
		recordEdge(e.queue, e.verb, strings.TrimSpace(line), expectCap)
		break
	}
	return n, err
}

// watchOutput wraps a verb's streams so the bound it hits files itself.
func watchOutput(queue, verb string, stdout, stderr io.Writer) (io.Writer, io.Writer) {
	if queue == "" {
		return stdout, stderr
	}
	shared := &edgeWatcher{w: stdout, queue: queue, verb: verb}
	return shared, &edgeWatcher{w: stderr, queue: queue, verb: verb, filed: false}
}
