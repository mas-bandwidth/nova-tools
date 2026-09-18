package main

// THE BUS'S STRUCTURED LINE, and the one field list it is allowed to hold.
//
// SPEC-LOGS.md Part 2 names the bus twice: once to say that every mechanical part emits its
// state changes, and once, under what must never be logged, to say that a note's body is
// prose with a scope and a set of readers -- log the note id, the scope and the receipt,
// never the body. This file is that rule as code. A note event carries the id, the lane it
// was written into, the number of recipients and their resolved names, the commit and
// whether it was pushed. It carries NO body, NO subject and NO path:
//
//   - the body is the thing the rule names outright;
//   - the SUBJECT is prose the writer wrote, one line of a private note, and a subject is
//     very often the whole point of the note ("the key is rotated to X"); and
//   - the PATH holds the slug, and the slug is minted FROM the subject, so a path on the
//     line is the subject on the line by another spelling.
//
// The recipients are names from the bus's own roster -- the scope the spec says to log --
// and not addresses of anybody's, so they stay.
//
// The human SEND OK / REPLY OK lines are untouched and still carry path and subject: they
// go to the operator running the verb, who is already reading the note. This line goes to
// Loki, where a reader is anybody with a dashboard.

import (
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/log"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// EventNote is the kind of the line a landed note writes: `{source="nova-bus"} | json |
// event="note"` is "what went out on the bus, to whom, in which lane".
const EventNote = "note"

// busRecipientCap is how many recipient names the line prints before `+<k>` stands for the
// rest. A line is one line: a broadcast to thirty names is a count and eight names, not a
// paragraph.
const busRecipientCap = 8

// busEmitter opens the verb's structured sink and wires the emitter. A --log that cannot be
// opened is a refusal here, before the checkout is touched, and never a silent send: a note
// that went out with no record of it having gone out is exactly the thing the stream exists
// to prevent.
func busEmitter(verb, token, logPath, bench string, stderr io.Writer) (*log.Emitter, io.Closer, bool) {
	// THE SINK IS THE FILE OR NOTHING. Round 1 let a verb with no --log write its JSON to
	// stderr, on the grounds that a unit's stderr is the journal. That is right for a loop
	// that runs as a unit and wrong for a verb like this one, whose stderr IS a contract:
	// other programs and this tree's own tests read its refusal lines, and a second line of
	// JSON beside a one-line refusal breaks them (found by dogfooding, 2026-09-18). The file
	// Alloy tails is the path (SPEC-LOGS.md Part 6), so a run that names no file writes no
	// structured line and every existing stdout and stderr contract is untouched.
	w, closer, err := log.Sink(logPath, nil)
	if err != nil {
		fmt.Fprintf(stderr, "%s REFUSED: --log %s cannot be opened for append: %s\n",
			token, oneline.Field(logPath), oneline.Err(err))
		return nil, nil, false
	}
	return log.NewEmitter(w, "nova-bus", verb, log.BenchName(bench)), closer, true
}

// emitNote writes one landed note as one event: the id, the lane, the recipients and the
// receipt. Every value is a field this program or the roster owns; nothing a writer typed
// as prose is on the line.
func emitNote(e *log.Emitter, id, lane string, to []string, commit string, pushed bool) {
	if e == nil {
		return
	}
	l := e.Line(EventNote)
	l.Job = oneline.Field(id) // the note id is this line's work item
	l.Msg = fmt.Sprintf("id=%s lane=%s to=%d names=%s commit=%s pushed=%t",
		oneline.Field(dash(id)), oneline.Field(dash(lane)), len(to),
		oneline.Field(recipientNames(to)), oneline.Field(dash(commit)), pushed)
	e.Send(l)
}

// emitNoteRefused writes the send or reply that did not land, with the reason this program
// wrote -- never the transcript, which is git's output about a file whose name is the slug.
func emitNoteRefused(e *log.Emitter, id, lane, reason string) {
	if e == nil {
		return
	}
	l := e.Line(log.EventRefuse)
	l.Level = "ERROR"
	l.Job = oneline.Field(id)
	l.Msg = fmt.Sprintf("id=%s lane=%s refused=%s",
		oneline.Field(dash(id)), oneline.Field(dash(lane)), oneline.Field(reason))
	e.Send(l)
}

// recipientNames is the capped, comma-joined roster names, or "-" when a note resolved to
// nobody (which the send itself already refuses; the line does not pretend otherwise).
func recipientNames(to []string) string {
	if len(to) == 0 {
		return "-"
	}
	shown := to
	more := 0
	if len(shown) > busRecipientCap {
		more = len(shown) - busRecipientCap
		shown = shown[:busRecipientCap]
	}
	out := strings.Join(shown, ",")
	if more > 0 {
		out += fmt.Sprintf("+%d", more)
	}
	return out
}
