/*
Package board is the owed-work layer: a board is the list of things a group of lines
owes, kept as an append-only log of events, and the list of open cards is DERIVED from
that log rather than stored anywhere. This file is the format — the five event lines,
rendered and parsed — and the two identities a board uses.

Two rules live here and nowhere else, because everything above them depends on both.

THE ID IS A DRAW, NEVER A DERIVATION. add reads 128 bits from the operating system's
random source and renders them as thirty-two lower-case hex characters. It is computed
from nothing — not the stamp, not the filer, not the text, not a count — so two adds
from two clones, under one name, at one second, with one text, get two ids without
reading anything first. An earlier draft derived the id from a hash over an unsynchronized
sequence number, which is a read two writers can both make before either writes; a
creation identity may depend on nothing of that kind.

PARSING IS ANCHORED AND BY ID. A line binds to a card only when it BEGINS with one of the
five verbs and the id is the token immediately after it. The prototype matched
`closed[ :]*<sid>` anywhere in any comment body, so on a board about a repository full of
issue numbers a card whose text held a six-digit number could be closed by a sentence
about something else.

Every key=value value on an event line goes through oneline.Field and the free text after
the ": " through oneline.Escape, so an event is one line whatever a filer pastes and a
whitespace-splitting scanner counts exactly the fields the tool wrote. The values a
parsed event carries are the ESCAPED ones — that is what the board holds — and rendering
them again is a fixed point, which is what makes a parsed event render back to the bytes
it was read from.
*/
package board

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Version is the first line of every card file: a later format read as this one would be
// entries nobody wrote.
const Version = "BOARD v1"

// IDHex and EvHex are the two identity widths. A card's id is 128 bits because two draws
// of it meeting is not an event this family will see; an event's is 48, which is enough
// to name a predecessor inside one card's history and short enough to read.
const (
	IDHex = 32
	EvHex = 12
)

// Verbs are the five event lines, and that is the whole format.
var Verbs = []string{"card", "taken", "closed", "landed", "probed"}

// Closing names the three closing events and the only three.
func Closing(verb string) bool { return verb == "closed" || verb == "landed" || verb == "probed" }

// Event is one line of a board's log.
//
// A field holds the value as it appears ON THE WIRE once it has been through Render: the
// creating caller hands raw text and Render escapes it, and a parsed event holds the
// escaped form, which Render leaves alone. So Parse(Render(e)).Render() == Render(e).
type Event struct {
	Verb     string // card, taken, closed, landed, probed
	ID       string // the card's thirty-two hex id
	Ev       string // this event's own twelve hex id; empty on card
	After    string // the newest ev in the writer's fold, or the card id; empty on card
	As       string // the --as of the verb that appended it: a label, never an identity
	At       time.Time
	AtRaw    string // what the line carried, kept when it will not parse
	AtOK     bool
	Override bool // true exactly when --anyway went over a refusal

	Hash     string // card: twelve hex over the rendered text, for a duplicate note only
	Owner    string // card: --owner, else the filer
	By       string // card: the deadline, stored as given
	Default  string // card: what happens if nobody closes it
	Thing    string // card: a row of the owed ledger
	Leg      string // card: the leg the row is owed on
	Evidence string // card: a path the filer named, carried and never opened
	In       string // landed: <repo>#<n>

	Tail string // card text, a close's how, a probe's evidence
	Line string // the bytes this event was parsed from, empty when it was built
}

// Stamp is the one time format on a board: RFC 3339 in UTC, to the second.
func Stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// Tail prepares a free-text tail for an event line: capped at oneline.TailBytes with the
// mark that says so, then escaped. Cap runs BEFORE the escape, on a rune boundary, so an
// escape sequence is never halved. A caller builds a tail once, at creation; Render does
// not cap, so that re-rendering a parsed event cannot cut a tail twice.
func Tail(s string) string { return oneline.Escape(oneline.Cap(s, oneline.TailBytes)) }

// HashOf is the content hash: the first twelve hex characters of a SHA-256 over the
// card's text AS RENDERED. The text only — not the leg, owner, deadline, default or
// evidence — and it is never an identity. It has one use: a note beside a filing that
// looks like one already open.
func HashOf(renderedText string) string {
	sum := sha256.Sum256([]byte(renderedText))
	return hex.EncodeToString(sum[:])[:EvHex]
}

// NewID draws a card's id: 128 bits from the source, thirty-two lower-case hex.
func NewID(r io.Reader) (string, error) { return draw(r, IDHex/2) }

// NewEv draws an event's id: 48 bits from the source, twelve lower-case hex.
func NewEv(r io.Reader) (string, error) { return draw(r, EvHex/2) }

// draw reads exactly n bytes or refuses. A short read is a refusal and never a short id:
// an id narrower than it claims to be is a collision nobody would look for.
func draw(r io.Reader, n int) (string, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", fmt.Errorf("reading %d bytes from the random source: %w", n, err)
	}
	return hex.EncodeToString(buf), nil
}

// Hex reports whether s is exactly n lower-case hex characters.
func Hex(s string, n int) bool {
	return len(s) == n && strings.Trim(s, "0123456789abcdef") == ""
}

// Render writes the event as its one line, without a newline.
func Render(e Event) string { return e.Render() }

// Render writes the event as its one line, without a newline. Every value goes through
// oneline.Field and the tail through oneline.Escape, so nothing a filer supplies can add
// a second line or pose as a field this tool did not write.
func (e Event) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", oneline.Field(e.Verb), oneline.Field(e.ID))
	if e.Verb != "card" {
		fmt.Fprintf(&b, " ev=%s after=%s", oneline.Field(e.Ev), oneline.Field(e.After))
	}
	fmt.Fprintf(&b, " as=%s at=%s override=%s", oneline.Field(e.As), oneline.Field(e.stamp()), boolField(e.Override))
	if e.Verb == "card" {
		fmt.Fprintf(&b, " hash=%s owner=%s by=%s default=%s",
			oneline.Field(e.Hash), oneline.Field(e.Owner), oneline.Field(e.By), oneline.Field(e.Default))
		if e.Thing != "" || e.Leg != "" {
			fmt.Fprintf(&b, " thing=%s leg=%s", oneline.Field(e.Thing), oneline.Field(e.Leg))
		}
		if e.Evidence != "" {
			fmt.Fprintf(&b, " evidence=%s", oneline.Field(e.Evidence))
		}
	}
	if e.Verb == "landed" {
		fmt.Fprintf(&b, " in=%s", oneline.Field(e.In))
	}
	if e.Verb != "landed" && e.Verb != "taken" {
		fmt.Fprintf(&b, ": %s", oneline.Escape(e.Tail))
	}
	return b.String()
}

// stamp is what the at= field carries: the parsed time when there is one, and otherwise
// exactly what the line held, because an unparseable stamp is never guessed at.
func (e Event) stamp() string {
	if e.AtRaw != "" {
		return e.AtRaw
	}
	return Stamp(e.At)
}

// boolField renders the two values override= and nothing else may take.
func boolField(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// Parse reads one line of a board's log. It returns false for anything that is not
// exactly one of the five events — a blank line, a comment, prose, a line in the shape an
// earlier draft used — and the caller COUNTS those rather than guessing at them.
func Parse(line string) (Event, bool) {
	raw := strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(raw) == "" || strings.HasPrefix(strings.TrimSpace(raw), "#") {
		return Event{}, false
	}
	head, tail, hasTail := strings.Cut(raw, ": ")
	toks := strings.Fields(head)
	if len(toks) < 2 {
		return Event{}, false
	}
	// ANCHORED: the verb is the line's first token and the id is the one after it.
	// Anything else is prose that mentions a card, which is not an event about it.
	if !strings.HasPrefix(raw, toks[0]+" ") {
		return Event{}, false
	}
	e := Event{Verb: toks[0], ID: toks[1], Tail: tail, Line: raw}
	known := false
	for _, v := range Verbs {
		known = known || v == e.Verb
	}
	if !known || !Hex(e.ID, IDHex) {
		return Event{}, false
	}
	seen := map[string]bool{}
	for _, tok := range toks[2:] {
		key, value, ok := strings.Cut(tok, "=")
		if !ok || seen[key] {
			return Event{}, false
		}
		seen[key] = true
		switch key {
		case "ev":
			e.Ev = value
		case "after":
			e.After = value
		case "as":
			e.As = value
		case "at":
			e.AtRaw = value
		case "override":
			switch value {
			case "true":
				e.Override = true
			case "false":
			default:
				return Event{}, false
			}
		case "hash":
			e.Hash = value
		case "owner":
			e.Owner = value
		case "by":
			e.By = value
		case "default":
			e.Default = value
		case "thing":
			e.Thing = value
		case "leg":
			e.Leg = value
		case "evidence":
			e.Evidence = value
		case "in":
			e.In = value
		default:
			return Event{}, false
		}
	}
	if e.As == "" || e.AtRaw == "" || !seen["override"] {
		return Event{}, false
	}
	if when, err := time.Parse(time.RFC3339, e.AtRaw); err == nil {
		e.At, e.AtOK = when.UTC(), true
	}
	switch e.Verb {
	case "card":
		if e.Ev != "" || e.After != "" || !hasTail || e.Hash == "" || e.Owner == "" || e.By == "" || e.Default == "" {
			return Event{}, false
		}
		if (e.Thing == "") != (e.Leg == "") {
			return Event{}, false
		}
	default:
		if !Hex(e.Ev, EvHex) || !(Hex(e.After, EvHex) || Hex(e.After, IDHex)) {
			return Event{}, false
		}
		if e.Hash != "" || e.Owner != "" || e.By != "" || e.Default != "" {
			return Event{}, false
		}
		if (e.Verb == "landed") != (e.In != "") {
			return Event{}, false
		}
		if hasTail != (e.Verb == "closed" || e.Verb == "probed") {
			return Event{}, false
		}
	}
	return e, true
}

// CreationFields is everything on a card line but the stamp: what an --id retry must
// match for the retry to be the same filing rather than a different one.
func (e Event) CreationFields() string {
	return strings.Join([]string{e.As, e.Hash, e.Owner, e.By, e.Default, e.Thing, e.Leg, e.Evidence, e.Tail}, "\x00")
}
