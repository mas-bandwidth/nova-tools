package ci

// events_log.go is the bridge's introspection half. SPEC-JOBS.md "Events, not ticks" put
// the four events on Redis pub/sub, where they are read once by the reactor and then gone;
// SPEC-LOGS.md Part 2 says every mechanical part writes one structured JSON line per state
// change BESIDE the human line it already writes. This file joins the two: every event
// nova-work events publishes on the bus is also emitted as one JSON line, so the fleet's
// Loki holds the history the bus does not keep and a person can ask what the bridge did
// without an ssh and a grep.
//
// Nothing here is a second logging system and nothing here ships anything. The line goes
// through internal/log (Go's own log/slog), to a writer the caller chose: stderr, which on
// a bench is the unit's stderr and so the systemd journal Alloy already reads, or the file
// --log names, which Alloy already tails. There is no new agent and no home-made shipper.
//
// The event KIND is the channel name -- card-done, pr-checks-done, dev-moved -- so the
// vocabulary a LogQL query selects on (`event="card-done"`) is the same vocabulary the bus
// carries, and the constants are the same constants. One event, one name, two places.

import (
	"io"
	"time"

	novalog "github.com/mas-bandwidth/nova-tools/internal/log"
)

// SourceEvents and VerbEvents are the source and verb labels every line the bridge writes
// carries. They are the tool and the verb a reader types, so `{source="nova-work",
// verb="events"}` is exactly "what did the bridge do".
const (
	SourceEvents = "nova-work"
	VerbEvents   = "events"
)

// The verb-level kinds, the SPEC-LOGS spine. A bridge that started and never finished is a
// start with no done, which is the whole hang test.
const (
	EventStart  = "start"
	EventDone   = "done"
	EventRefuse = "refuse"
)

// emit writes one structured line for one published event. A nil Events writer writes
// nothing, which is how every caller and every test that predates this slice keeps its
// exact stdout and stderr: the line is additive, never a replacement.
//
// The clock and the guid are the producer's injected ones, so a test is deterministic and
// never reads time.Now or /proc.
func (p *Producer) emit(event, msg, card string, pr int) {
	p.write(event, msg, card, pr, "INFO", 0, nil)
}

// Announce writes one verb-level line -- start, done or refuse -- with the elapsed time
// and, on a failure, the error. It is exported because the verb's own spine is written by
// cmd/nova-work, where the flags and the deadline live, while the per-event lines are
// written here, where the publishes happen.
func (p *Producer) Announce(event, msg string, dur time.Duration, err error) {
	level := "INFO"
	if err != nil {
		level = "ERROR"
	}
	p.write(event, msg, "", 0, level, dur, err)
}

// write is the one place a Line is built, so every line the bridge emits carries the same
// five labels and the same fixed fields whatever wrote it.
func (p *Producer) write(event, msg, card string, pr int, level string, dur time.Duration, err error) {
	if p.Events == nil {
		return
	}
	clock := p.Clock
	if clock == nil {
		clock = time.Now
	}
	guid := p.GUID
	if guid == nil {
		guid = novalog.ProcessGUID
	}
	l := novalog.New(clock, guid, SourceEvents)
	l.Level = level
	l.Verb = VerbEvents
	l.Bench = p.Bench
	l.Event = event
	l.Card = card
	l.PR = pr
	l.Msg = msg
	l.DurMS = dur.Milliseconds()
	if err != nil {
		l.Err = err.Error()
	}
	// A log that cannot be written is not a reason to fail the publish: the bus event
	// already happened, and the record of it is the bus, not this line.
	_ = l.Write(p.Events)
}

// EventSink is the writer a caller hands the producer. It exists as a name so the verb's
// two shapes -- stderr, and the file --log names -- read as the one decision they are.
type EventSink = io.Writer
