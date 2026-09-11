package board

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

const (
	idA = "0123456789abcdef0123456789abcdef"
	idB = "fedcba9876543210fedcba9876543210"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// Work list 1: a card whose TEXT contains a six-digit number cannot be closed by a
// sentence about another card. The prototype matched `closed[ :]*<sid>` anywhere in any
// comment body; parsing here is anchored at the line start with the id as the token
// immediately after the verb.
func TestParsingIsAnchoredAndByIdNeverBySubstring(t *testing.T) {
	card := Event{Verb: "card", ID: idA, As: "rowan", At: at("2026-09-11T10:00:00Z"),
		Hash: "aaaaaaaaaaaa", Owner: "rowan", By: "2026-09-11T14:00:00Z",
		Default: "rowan files it", Tail: "the windows runner skips three steps, see 176549"}
	line := card.Render()
	if !strings.HasPrefix(line, "card "+idA+" ") {
		t.Fatalf("a card line must begin with its verb and its id: %q", line)
	}
	for _, prose := range []string{
		"I closed 176549 this morning, which was about the same thing",
		"taken by somebody: closed " + idA + " maybe",
		"  closed " + idA,
	} {
		if ev, ok := Parse(prose); ok {
			t.Errorf("prose bound to a card as %s: %q", ev.Verb, prose)
		}
	}
	taken := Event{Verb: "taken", ID: idA, Ev: "0123456789ab", After: idA, As: "bo", At: at("2026-09-11T10:01:00Z")}
	got, ok := Parse(taken.Render())
	if !ok {
		t.Fatalf("a taken line this package rendered does not parse: %q", taken.Render())
	}
	if got.ID != idA || got.As != "bo" || got.Verb != "taken" || got.After != idA {
		t.Errorf("round trip lost a field: %+v", got)
	}
}

// The one-line guarantee over a card's text: a filer who pastes a newline files ONE card.
func TestATextWithANewlineFilesOneCard(t *testing.T) {
	card := Event{Verb: "card", ID: idA, As: "rowan", At: at("2026-09-11T10:00:00Z"),
		Hash: "aaaaaaaaaaaa", Owner: "ada vale", By: "2026-09-11T14:00:00Z",
		Default: "rowan files it anyway", Tail: Tail("two lines\nand a second")}
	line := card.Render()
	if strings.Contains(line, "\n") {
		t.Fatalf("the rendered event holds a newline: %q", line)
	}
	if !strings.Contains(line, `\x0a`) {
		t.Errorf("the newline was not escaped: %q", line)
	}
	if !strings.Contains(line, `owner=ada\x20vale`) {
		t.Errorf("a field value holding a space is not one token: %q", line)
	}
	if !strings.Contains(line, `default=rowan\x20files\x20it\x20anyway`) {
		t.Errorf("the default is not one token: %q", line)
	}
	back, ok := Parse(line)
	if !ok {
		t.Fatalf("the rendered card does not parse: %q", line)
	}
	if back.Render() != line {
		t.Errorf("re-rendering a parsed event changed it:\n%s\n%s", line, back.Render())
	}
}

// An unparsed line is counted and never guessed at.
func TestAnUnparsedLineIsCountedAndNeverGuessedAt(t *testing.T) {
	card := Event{Verb: "card", ID: idA, As: "rowan", At: at("2026-09-11T10:00:00Z"),
		Hash: "aaaaaaaaaaaa", Owner: "rowan", By: "2026-09-11T14:00:00Z", Default: "d", Tail: "a thing"}
	log := Log{Lines: []string{
		card.Render(),
		"closed (2026-09-11T10:05:00Z, bo) by bo: an old-shape line",
		"taken " + idA,
		"",
		"# a comment",
	}}
	b := Derive(log, at("2026-09-11T10:06:00Z"), time.Minute)
	if b.Unparsed != 2 {
		t.Errorf("unparsed = %d, want 2 (the old-shape close and the truncated take)", b.Unparsed)
	}
	if len(b.Cards) != 1 || b.Cards[0].State != "OPEN" {
		t.Errorf("an unparsed line was guessed at as a close: %+v", b.Cards)
	}
}

// The id is 128 bits from an injectable source, and it is a DRAW: nothing computed from
// the fields.
func TestTheIdIsAHundredAndTwentyEightBitsFromTheSource(t *testing.T) {
	src := bytes.NewReader(bytes.Repeat([]byte{0xab}, 64))
	id, err := NewID(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
		t.Fatalf("id = %q, want thirty-two lower-case hex characters", id)
	}
	if id != strings.Repeat("ab", 16) {
		t.Errorf("id = %q; it must be the source's bytes, not a hash over them", id)
	}
	ev, err := NewEv(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) != 12 || strings.Trim(ev, "0123456789abcdef") != "" {
		t.Fatalf("ev = %q, want twelve lower-case hex characters", ev)
	}
	// A source that runs out is a refusal, never a short id.
	if _, err := NewID(bytes.NewReader(nil)); err == nil {
		t.Error("an exhausted random source produced an id")
	}
}

// The hash is over the text only, and it is never the identity.
func TestTheContentHashIsOverTheTextAlone(t *testing.T) {
	h := HashOf(Tail("the windows runner skips three steps"))
	if len(h) != 12 || strings.Trim(h, "0123456789abcdef") != "" {
		t.Fatalf("hash = %q, want twelve lower-case hex characters", h)
	}
	if h == HashOf(Tail("something else")) {
		t.Error("two different texts hashed the same")
	}
	if h != HashOf(Tail("the windows runner skips three steps")) {
		t.Error("the hash is not a function of the text alone")
	}
}
