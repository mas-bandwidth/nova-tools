package sprintwire

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"
	"sync"
	"time"
)

// Worker runs a worker's sprint verbs on the sprint's server: each verb's
// arguments are sent and its exit code and what it printed come back, as when
// the worker ran nova-sprint itself, so a member is the same loop over either.
// The worker's machine reads and writes nothing of the store.
type Worker struct {
	// Send delivers verbs to the server in one request: Client.Do in
	// production, the server's own step in a test (no connection).
	Send func(ctx context.Context, verbs ...[]string) ([]Result, error)
	// Failed shapes what a verb that did not exit 0 printed, for the member's
	// log; nil is its stderr, then its stdout.
	Failed func(stdout, stderr []byte) []byte
	// Budget bounds one verb, its tries together; 0 is Timeout.
	Budget time.Duration
	// load is the last load a beat named: the server cannot measure this
	// machine, so a beat sent with none (no sample since the last) names the
	// last again; mu guards it (the beat has its own goroutine).
	mu   sync.Mutex
	load string
}

// Tries is how many times a verb is sent when the server does not answer. A
// write carries one operation id through them, so one that ran and whose answer
// was lost returns its recorded result and changes nothing twice.
const Tries = 3

// Run sends one verb and returns its exit code and output; 2, with why, when
// the server did not answer. The server's own refusals may exit 2 as well
// (nova-sprint's refuse() returns 2, and serve passes the code through), so the
// code alone does not say what became of the write: RunAnswer does.
func (w *Worker) Run(args ...string) (int, []byte) {
	code, out, _ := w.RunAnswer(args...)
	return code, out
}

// Answer is what a verb's run says became of its write, beside its exit code.
type Answer int

const (
	// Accepted is exit 0: the server answered and the verb did what it named.
	Accepted Answer = iota
	// Refused is a non-zero exit the server answered for a write that did not
	// happen: a card refused (stepExit 1), a usage or epoch refusal before the
	// store was touched (refuse(), exit 2, "nova-sprint <verb> REFUSED:"), a
	// worker verb the server will not serve, a pending operation, a cleared
	// epoch that changed nothing.
	Refused
	// Unknown is a write whose outcome is not known: no try reached the server
	// (the 2 is this worker's own), or the server answered that the store did not
	// confirm it (ErrUnknown, changed=unknown), that its operation was cut after
	// an earlier manifest applied (CutError, fenced for nova-sprint repair), or
	// that it finished at an epoch a clear closed.
	Unknown
)

// The words nova-sprint prints for the answers whose write may have happened
// (nova-sprint cmd/nova-sprint/verbs.go report and internal/sprint/store):
// ErrUnknown's sentence and the status line's changed=unknown; CutError's
// "operation <op> cut at table"; ClearedError's "as this step finished".
var unknownMarks = []string{
	"changed=unknown",
	"the store did not confirm the write",
	" cut at table ",
	"as this step finished",
}

// Classify is the Answer of a verb that exited code having printed stdout and
// stderr, answered saying whether the server answered it at all.
func Classify(code int, stdout, stderr []byte, answered bool) Answer {
	switch {
	case !answered:
		return Unknown
	case code == 0:
		return Accepted
	}
	for _, m := range unknownMarks {
		if bytes.Contains(stderr, []byte(m)) || bytes.Contains(stdout, []byte(m)) {
			return Unknown
		}
	}
	return Refused
}

// RunAnswer is Run with what became of the write (Classify): the server's own
// result read whole, before Failed shapes it for the log, so a capped log line
// never decides it.
func (w *Worker) RunAnswer(args ...string) (code int, out []byte, answer Answer) {
	args = slices.Clone(args)
	if len(args) >= 2 && args[0] == "fleet" && args[1] == "beat" {
		if args = w.beat(args); args == nil {
			return 1, []byte("beat not sent: this machine's load has no sample yet, and the server cannot measure it; the next beat names one"), Refused
		}
	}
	if len(args) > 0 && (args[0] == "take" || args[0] == "finish" || args[0] == "read") {
		args = append(joinText(args), "--op", newOp())
	}
	budget := w.Budget
	if budget == 0 {
		budget = Timeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	var err error
	for try := 0; try < Tries && ctx.Err() == nil; try++ {
		var res []Result
		if res, err = w.Send(ctx, args); err != nil {
			continue
		}
		r := res[0]
		answer = Classify(r.Code, []byte(r.Stdout), []byte(r.Stderr), true)
		if r.Code != 0 {
			return r.Code, w.failed([]byte(r.Stdout), []byte(r.Stderr)), answer
		}
		return 0, []byte(r.Stdout), answer
	}
	return 2, w.failed(nil, []byte(err.Error())), Unknown
}

func (w *Worker) failed(stdout, stderr []byte) []byte {
	if w.Failed != nil {
		return w.Failed(stdout, stderr)
	}
	return append(slices.Clone(stderr), stdout...)
}

// beat is a fleet beat as the server takes it, `fleet beat <member> --load
// <percent>`: the load the member named, remembered, or when it named none the
// last it named; nil before it has named any (no load is invented).
func (w *Worker) beat(args []string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := []string{"fleet", "beat"}
	for i := 2; i < len(args); i++ {
		switch v, eq := strings.CutPrefix(args[i], "--load="); {
		case eq:
			w.load = v
		case args[i] == "--load" && i+1 < len(args):
			i++
			w.load = args[i]
		default:
			out = append(out, args[i])
		}
	}
	if w.load == "" {
		return nil
	}
	return append(out, "--load", w.load)
}

// joinText is the verb with each flag that carries free text (a report, a
// finding, a reason, a usage line, a head, a branch) joined to its value as
// --flag=value. The server refuses any word of a verb that is a flag naming a
// store, an actor or a worker, wherever it stands; a child's words are its own
// (a finding may begin `--as=`), and joined to their flag they are one word that
// is plainly that flag's value.
func joinText(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--report", "--finding", "--reason", "--usage", "--head", "--branch", "--base":
			if i+1 < len(args) {
				out = append(out, args[i]+"="+args[i+1])
				i++
				continue
			}
		}
		out = append(out, args[i])
	}
	return out
}

// newOp is an operation id for one verb sent to the server.
func newOp() string {
	var b [8]byte
	_, _ = rand.Read(b[:]) // ignored: crypto/rand.Read does not fail on the supported platforms
	return "w-" + hex.EncodeToString(b[:])
}
