package main

// events.go is the merge lane's half of SPEC-LOGS.md Part 2: the structured line every
// lane verb writes BESIDE the human line it already writes.
//
// #1326 put nova-work events on the stream and left two panels of the fleet dashboard
// reading the status page's metrics.tsv, because the numbers they drew -- the merge queue's
// depth and the ready-card count -- were not events. They are now, and they are emitted
// where they are already KNOWN rather than derived from the bus: the queue's depth by the
// verb that opens the queue file, the ready count by the tick that reads the directory.
// A number guessed from the bus would be a number nobody could check.
//
// THE KINDS ARE NOUNS OF THIS LANE, one per state change a reader asks about:
//
//	batch-start     an integration batch began, on a named base at a named sha
//	batch-member    one pull request merged into it, or was dropped with the reason
//	batch-verdict   the gate's answer: OK, or FAIL with the step, packages and tests
//	batch-enqueued  the branch a green batch built, and the head a caller pushes
//	queue-depth     running, waiting and unmergeable, on every read of the queue
//	queue-audit     one entry whose automatic merge the queue turned off, and why
//	the channel name the reactor reacted to (card-done, pr-checks-done, dev-moved)
//
// plus internal/log's verb spine -- start, done, refuse -- so a verb that began and never
// finished is a start with no done rather than a silence.
//
// THE NUMBERS LIVE IN THE MESSAGE, as `name=value` pairs. The line's fifteen fields are
// fixed by the spec and a sixteenth would break every `| json` query the fleet already
// runs, so a panel that wants a number reads it with `line_format "{{.msg}}"` and a regexp,
// which is what fleet/logstack/nova-events-dashboard.json does.

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/log"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// sourceMerge is the source label on every line this binary writes: the tool a reader
// types, so `{source="nova-merge", verb="queue"}` is exactly "what did the queue do".
const sourceMerge = "nova-merge"

// The lane's own event kinds.
const (
	eventBatchStart    = "batch-start"
	eventBatchMember   = "batch-member"
	eventBatchVerdict  = "batch-verdict"
	eventBatchEnqueued = "batch-enqueued"
	eventQueueDepth    = "queue-depth"
	eventQueueAudit    = "queue-audit"
)

// openEmitter is the one wiring every emitting verb in this binary does: the sink, the
// three labels, and the injected clock.
//
// The sink's default is stderr, which under systemd is the unit's journal and so a source
// Alloy already reads without a new agent; --log names the file Alloy tails instead. A path
// that cannot be opened is a REFUSAL and never a silent run with no log -- a bench whose
// lines never reach Loki must say why, at the start, which is why this returns an exit code
// a caller returns rather than an emitter that quietly writes nowhere.
//
// The returned func is the caller's defer: it closes the file when --log opened one and
// does nothing when the fallback was taken, because the caller's stderr is the caller's.
func openEmitter(verb, bench, logPath string, stderr io.Writer, deps Deps) (*log.Emitter, func(), int) {
	w, closer, err := log.Sink(logPath, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "%s REFUSED: --log %s cannot be opened for append: %s\n",
			strings.ToUpper(verb), oneline.Field(logPath), oneline.Err(err))
		return nil, func() {}, 2
	}
	e := log.NewEmitter(w, sourceMerge, verb, log.BenchName(bench))
	if deps.Now != nil {
		e.Clock = deps.Now
	}
	done := func() {}
	if closer != nil {
		done = func() { _ = closer.Close() }
	}
	return e, done, 0
}

// emitPR is one event about one pull request: the number goes in the line's own `pr` field,
// where a query reaches it with `| json`, and never only in the sentence.
func emitPR(e *log.Emitter, event string, pr int, msg string) {
	l := e.Line(event)
	l.PR = pr
	l.Msg = msg
	e.Send(l)
}

// emitErr is one event carrying an error at ERROR, so a red panel is a label selector and
// not a line filter.
func emitErr(e *log.Emitter, event, msg string, err error) {
	e.Announce(event, msg, 0, err)
}

// refuseEvent is the verb spine's refusal, with the elapsed time from the verb's own start.
func refuseEvent(e *log.Emitter, msg string, start time.Time, err error) {
	e.Announce(log.EventRefuse, msg, time.Since(start), err)
}

// THE LANE'S DEPTH, IN ONE DEFINITION, emitted by every verb that opens the queue.
//
// The three numbers are the three answers a person wants from the panel that replaced the
// metrics.tsv one, and each is read off the queue file rather than guessed from the bus:
//
//	running      the entry the lane is acting on right now. The lane merges ONE entry at a
//	             time, so it is the head of the queue -- and zero while a hold stands,
//	             because a held lane is acting on nothing at all.
//	waiting      the queued entries behind that head, which is the depth a person means
//	             when they ask how long their pull request will sit there.
//	unmergeable  the entries the lane holds and will not merge as they stand: the skipped
//	             and the parked, counted once each, because the sweep parks an entry INTO
//	             the skip set and one pull request is one number on the panel.
func queueDepth(q *merge.Queue, held bool) (running, waiting, unmergeable int) {
	if len(q.Queued) > 0 && !held {
		running = 1
	}
	waiting = len(q.Queued) - running
	stuck := map[int]bool{}
	for _, pr := range q.Skipped {
		stuck[pr] = true
	}
	for _, p := range q.Parked {
		stuck[p.PR] = true
	}
	return running, waiting, len(stuck)
}

// emitQueueDepth writes that depth as one queue-depth event. The subverb that read the
// queue is on the line, so a reader can tell a sweep's depth from a hold's.
func emitQueueDepth(e *log.Emitter, sub string, q *merge.Queue, held bool) {
	running, waiting, unmergeable := queueDepth(q, held)
	e.Emit(eventQueueDepth, fmt.Sprintf("sub=%s running=%d waiting=%d unmergeable=%d held=%t",
		oneline.Field(sub), running, waiting, unmergeable, held))
}

// emitQueueAudit is ONE ENTRY WHOSE AUTOMATIC MERGE THE QUEUE TURNED OFF, and why.
//
// It is the audit trail the house rule needs (Glenn, 2026-09-11: merge only after the
// checks show zero failures; never `--auto`). An entry stops merging by a hand -- a skip --
// or by the sweep's own judgment -- a park on a poison test -- or the whole lane stops at
// once on a hold, which carries no entry number because it is not about one. A stop nobody
// wrote a reason for is the thing this event exists to prevent.
func emitQueueAudit(e *log.Emitter, pr int, action, reason string) {
	emitPR(e, eventQueueAudit, pr, fmt.Sprintf("action=%s pr=%d reason=%s",
		oneline.Field(action), pr, oneline.Escape(reason)))
}
