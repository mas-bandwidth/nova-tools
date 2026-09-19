package log

// emit.go is the ONE emitter every mechanical part writes its structured line through.
//
// SPEC-LOGS.md Part 2 fixes the shape and log.go builds it; this file is the shape a VERB
// holds: a source, a verb and a bench that do not change for the length of a run, and a
// writer that is either the process's stderr -- which under systemd is the unit's journal
// and so a source Alloy already reads -- or the file --log names, which Alloy tails.
//
// It exists because the second and third part to emit would otherwise each grow their own
// half of internal/ci/events_log.go. The producer of #1326 was the first; nova-merge batch,
// nova-merge queue, nova-merge react and nova-pulse fill are the next four, and all five
// build their lines here. One emitter, one redaction, one field list: a line that reaches
// Loki from any of them answers the same LogQL query.
//
// A nil *Emitter and a nil W both write nothing, which is how a caller that named no sink
// keeps its exact stdout and stderr. The line is additive and never a replacement: a
// reader that only knows the human line still works.

import (
	"io"
	"os"
	"strings"
	"time"
)

// The verb-level spine of SPEC-LOGS.md Part 2. A verb that started and never finished is a
// start with no done, which is what makes a hang visible in a query rather than in an ssh.
// internal/ci's EventStart, EventDone and EventRefuse are these, so the bridge and the
// merge lane spell the spine the same way.
const (
	EventStart  = "start"
	EventDone   = "done"
	EventRefuse = "refuse"
)

// Emitter is one verb's structured sink for the length of its run.
//
// Clock and GUID are injected for the same reason they are injected in New: a test must be
// deterministic and must never read time.Now or /proc. Both default when they are nil, so
// production wiring is NewEmitter and nothing else.
type Emitter struct {
	W      io.Writer // nil writes nothing
	Clock  Clock
	GUID   GUIDSource
	Source string // the tool: nova-merge, nova-pulse, nova-work
	Verb   string // the verb within it: batch, queue, react, fill, events
	Bench  string // the machine, by its fleet name
}

// NewEmitter is the production wiring: the sink the verb chose, and the three labels that
// do not change for the length of the run. A nil writer is a quiet emitter.
func NewEmitter(w io.Writer, source, verb, bench string) *Emitter {
	return &Emitter{W: w, Source: source, Verb: verb, Bench: bench}
}

// Line is one line of this verb's, prefilled with everything that is the run's rather than
// the event's. The caller fills the ids it has -- a pr, a card, a job -- and the message,
// then hands it back to Write. The fields it does not fill stay "" or 0, which is the
// spec's "not this scope" and not an omitted key.
func (e *Emitter) Line(event string) Line {
	clock := time.Now
	guid := ProcessGUID
	if e != nil {
		if e.Clock != nil {
			clock = e.Clock
		}
		if e.GUID != nil {
			guid = e.GUID
		}
	}
	source, verb, bench := "", "", ""
	if e != nil {
		source, verb, bench = e.Source, e.Verb, e.Bench
	}
	l := New(clock, guid, source)
	l.Verb = verb
	l.Bench = bench
	l.Event = event
	return l
}

// Send renders one line to the sink. A log that cannot be written is never a reason to
// fail the work: the state change already happened, and the record of it is the lane, the
// queue or the bus, not this line.
//
// IT IS SEND AND NOT WRITE on purpose: every binary in this tree carries the one-line
// tripwire of internal/oneline/audit, which refuses a call named Write on any selector --
// a .Write is bytes going past the escaping path, and the whole point of this one is that
// it does NOT, because Line.Write escapes and redacts every field first. A method with the
// refused name would have made every emitting verb argue with the tripwire at its call
// site. One rename is cheaper than twenty exemptions.
func (e *Emitter) Send(l Line) {
	if e == nil || e.W == nil {
		return
	}
	_ = l.Write(e.W)
}

// Emit is the short form for an event with nothing but a message: the shape most of a
// verb's per-item lines take.
func (e *Emitter) Emit(event, msg string) {
	l := e.Line(event)
	l.Msg = msg
	e.Send(l)
}

// Announce writes the verb's own spine -- start, done or refuse -- with the elapsed time
// and, on a failure, the error. An announce carrying an error is ERROR, so a red panel is
// a label selector and not a line filter.
func (e *Emitter) Announce(event, msg string, dur time.Duration, err error) {
	l := e.Line(event)
	l.Msg = msg
	l.DurMS = dur.Milliseconds()
	if err != nil {
		l.Level = "ERROR"
		l.Err = err.Error()
	}
	e.Send(l)
}

// Sink is the one decision every emitting verb makes about where its lines go: stderr by
// default, or the file --log names.
//
// A path that cannot be opened is an error here and never a silent run with no log -- a
// bench whose lines never reach Loki must say why, at the start. The file is opened for
// APPEND, because the file Alloy tails is a log and not this run's output, and because two
// verbs on one bench may name the same file.
//
// The closer is nil when the fallback was taken: the caller's stderr is the caller's, and
// closing it here would take the process's own error stream out from under it.
func Sink(path string, fallback io.Writer) (io.Writer, io.Closer, error) {
	if path == "" {
		return fallback, nil, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}
	return f, f, nil
}

// BenchName is the bench label on every structured line: the flag when given, else
// $NOVA_BENCH, else the short hostname. It is the fleet's name for this machine, which is
// what a LogQL query selects on and what the dashboard's `bench` variable lists.
//
// It is the one reader of that variable in the tree, and a verb calls it exactly once, at
// wiring time, with the value of its own --bench. Nothing below the Emitter reads the
// environment: a test injects the label like any other field.
func BenchName(flagValue string) string {
	if s := strings.TrimSpace(flagValue); s != "" {
		return s
	}
	if s := strings.TrimSpace(os.Getenv("NOVA_BENCH")); s != "" {
		return s
	}
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	if i := strings.Index(h, "."); i > 0 {
		h = h[:i]
	}
	return strings.TrimSpace(h)
}
