/*
Package log is the one structured event line every part writes BESIDE the one human line
it already writes. SPEC-LOGS.md Part 2 fixes the shape: one JSON object per state change,
one line, through Go's own log/slog with slog.NewJSONHandler. The stdout line stays the
SPEC.md event, unchanged; this line is the same event with the fields a LogQL query needs,
so a person can ask "why is this card hung" without an ssh and a grep.

The fields are fixed, all fifteen of them, and every object carries all fifteen: an id
that is not this event's scope is the empty string or zero rather than an omitted key, so
`| json` never guesses and a query on `card=""` means exactly "not this scope". ts, level,
source, event and msg are never absent; the rest are "" or 0 when they do not apply.

The two fields that come from outside the event are injected: the clock that fills ts
(never time.Now() read directly, so a test is deterministic) and the source of the run's
guid (never /proc read directly in a test).

Nothing here is a second logging system. The handler is slog's, the escape is internal's
oneline, and the line is additive: a reader that only knows the stdout event still works.
*/
package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Line is one structured event: the fixed field list of SPEC-LOGS.md Part 2. It is a
// value, built by New and filled by the verb, and Write renders it as one JSON line.
type Line struct {
	TS     string // ts: UTC RFC3339Nano from the writer's clock
	Level  string // level: DEBUG, INFO, WARN or ERROR
	Source string // source: the tool that wrote the event
	Bench  string // bench: the machine, by its fleet name
	Verb   string // verb: the verb within the tool
	Job    string // job: the work item's job id
	Card   string // card: the work item's card id
	PR     int    // pr: the work item's pull request number
	Run    string // run: the work item's run id
	Slot   string // slot: the work item's slot
	GUID   string // guid: one per process run
	Event  string // event: start, refuse, retry, done, or the part's own noun
	Msg    string // msg: one human sentence, escaped through oneline by Write
	DurMS  int64  // dur_ms: milliseconds from start to this event
	Err    string // err: the error's text, escaped through oneline by Write
}

// Clock is the writer's clock. Production passes time.Now; a test passes a fixed func so
// ts is deterministic.
type Clock func() time.Time

// GUIDSource is the source of this process run's guid. Production passes ProcessGUID; a
// test passes a fixed func so it never reads /proc.
type GUIDSource func() string

// New fills the two fields that come from outside the event -- ts from the injected clock
// and guid from the injected source -- and defaults the level to INFO, the level of a
// state change that is not a warning or an error. The verb fills the rest.
func New(clock Clock, guid GUIDSource, source string) Line {
	return Line{
		TS:     clock().UTC().Format(time.RFC3339Nano),
		Level:  "INFO",
		Source: source,
		GUID:   guid(),
	}
}

// Write emits the event as exactly one JSON object with the spec's fifteen fields. The
// message and the error go through internal/oneline's Escape first, so a newline in either
// cannot add a second line and the object is one line whatever the sentence holds.
//
// ESCAPE AND NOT FIELD, for the two of them. Field is the form a SCANNER reads as one
// token: it escapes every space and every "=" as well, which is right for a value inside
// the human one-line grammar and wrong here, where JSON already delimits the value and the
// msg is a sentence a person reads -- `name\x3dintegration-1\x20prs\x3d3` is not a
// sentence, and a panel that reads a number out of the message cannot match it either.
// The ids keep Field, because each of them IS one token. The writer is
// injected: stderr in production (a unit's stderr is the systemd journal), a buffer in a
// test. The handler is slog's JSON handler; the handler's own time, level and msg keys are
// replaced by the spec's ts, level and msg fields so the object is the spec's object and
// not slog's.
//
// Every field whose content comes from outside this program -- the ids, the message and
// the error -- passes through Redact on the way out, so SPEC-LOGS.md Part 2's one hard
// rule ("a secret VALUE is never logged") is enforced by the emitter and not by review.
// The fixed vocabulary the program writes itself -- ts, level, source, event, guid -- is
// never touched, so a redaction can never rename an event out from under a query.
func (l Line) Write(w io.Writer) error {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	r := slog.NewRecord(time.Time{}, levelOf(l.Level), Redact(oneline.Escape(l.Msg)), 0)
	r.AddAttrs(
		slog.String("ts", l.TS),
		slog.String("source", l.Source),
		slog.String("bench", Redact(l.Bench)),
		slog.String("verb", l.Verb),
		slog.String("job", Redact(l.Job)),
		slog.String("card", Redact(l.Card)),
		slog.Int("pr", l.PR),
		slog.String("run", Redact(l.Run)),
		slog.String("slot", Redact(l.Slot)),
		slog.String("guid", l.GUID),
		slog.String("event", l.Event),
		slog.Int64("dur_ms", l.DurMS),
		slog.String("err", Redact(oneline.Escape(l.Err))),
	)
	return h.Handle(context.Background(), r)
}

// levelOf is the spec's level word as slog's level, so the handler writes its one spelling
// of it. The spec admits DEBUG, INFO, WARN and ERROR; anything else is the level of a
// state change that is not a warning or an error, which is INFO.
func levelOf(s string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// ProcessGUID is the production guid: the kernel's boot id, this process's pid and the
// process's start time. A process keeps the same guid for its whole run, so its lines
// group, and two runs that share a pid after a reboot still differ by the boot id. A test
// never calls this; it injects a fixed GUIDSource instead.
func ProcessGUID() string {
	return fmt.Sprintf("%s-%d-%s", bootID(), os.Getpid(), processStart())
}

// bootID reads the kernel's boot id, and names the absence rather than returning nothing
// from something. /proc is Linux's; on another platform the read fails and the pid and
// start still separate two runs.
func bootID() string {
	if raw, err := os.ReadFile("/proc/sys/kernel/random/boot_id"); err == nil {
		if s := strings.TrimSpace(string(raw)); s != "" {
			return s
		}
	}
	return "noboot"
}

// processStart is the process's start time in clock ticks, field 22 of /proc/<pid>/stat.
// The comm field in parentheses may hold spaces and parentheses, so the fields after the
// LAST ")" are counted, where field 3 is the first.
func processStart() string {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", os.Getpid()))
	if err != nil {
		return "nostart"
	}
	s := string(raw)
	if i := strings.LastIndex(s, ")"); i >= 0 {
		// fields[0] is field 3 (state); starttime is field 22, so index 22-3 = 19.
		if fields := strings.Fields(s[i+1:]); len(fields) > 19 {
			return fields[19]
		}
	}
	return "nostart"
}
