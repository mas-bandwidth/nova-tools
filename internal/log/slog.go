/*
slog.go is the shape a long-running verb holds.

SPEC-LOGS.md Part 2 fixes one shape for every mechanical part: one JSON object per state
change, one line. A verb that takes one unit of work and exits -- `nova-pulse launch`,
`nova-merge batch` -- builds a Line, writes it and is gone. A long-running verb is
different: it changes state many times over one run -- the harvest loop, the pull worker,
the bridge that republishes the bus -- and the labels that name the run do not change from
the first event to the last.

What does not change is what this file holds: the host the run is on (the spec's `bench`
field), the verb's own name, and the tool it belongs to. What changes is the event: the
label a LogQL query selects on and the one human sentence. Event renders one spec Line
through slog's JSON handler, so a line from a long-running verb is the same object -- the
same fifteen fields, escaped through oneline and redacted the same way -- as one written by
New and Write. Nothing here is a second logging system.
*/
package log

import (
	"io"
	"time"
)

// LongVerb is one long-running verb's structured sink for the length of its run.
//
// W nil writes nothing, which is how a caller that named no sink keeps its exact stdout and
// stderr: the line is additive and never a replacement. Clock and GUID are injected -- the
// same two the spec injects everywhere -- so a test is deterministic and never reads
// time.Now or /proc. A nil Clock or GUID defaults, so production wiring is the sink and the
// labels and nothing else.
type LongVerb struct {
	W      io.Writer // nil writes nothing
	Clock  Clock     // nil defaults to time.Now
	GUID   GUIDSource
	Source string // the tool: nova-pulse, nova-swarm, nova-work
	Host   string // the machine, by its fleet name -- the spec's bench field
	Verb   string // the verb within the tool: harvest, claim, launch
}

// NewLongVerb is the production wiring: the writer the verb chose, the tool it is, the host
// it runs on, its own verb name, and the injected clock and guid. A nil writer is a quiet
// verb, so a run that named no --log keeps its exact stdout and stderr.
func NewLongVerb(w io.Writer, source, host, verb string, clock Clock, guid GUIDSource) *LongVerb {
	return &LongVerb{W: w, Source: source, Host: host, Verb: verb, Clock: clock, GUID: guid}
}

// Event writes one state change as exactly one JSON line. label is the state change a
// LogQL query selects on -- start, done, refuse, retry, or the part's own noun (delete,
// keep, fetch, claim, ack) -- and msg is the one human sentence. The run's host, verb and
// source ride on every line, so `{source="nova-pulse", verb="harvest", bench="space"}`
// returns exactly one run's log.
//
// The line goes through Line.Write, so it carries the spec's fixed fifteen fields whether
// or not the caller filled them, redacts every field that came from outside the program,
// and cannot be split by a newline in msg.
//
// A line that cannot be written is never a reason to stop the work: the state change
// already happened, and the record of it is the loop or the lane, not this line.
func (v *LongVerb) Event(label, msg string) {
	if v == nil || v.W == nil {
		return
	}
	clock, guid := v.Clock, v.GUID
	if clock == nil {
		clock = time.Now
	}
	if guid == nil {
		guid = ProcessGUID
	}
	l := New(clock, guid, v.Source)
	l.Bench = v.Host
	l.Verb = v.Verb
	l.Event = label
	l.Msg = msg
	_ = l.Write(v.W)
}
