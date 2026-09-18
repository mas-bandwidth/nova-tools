package main

// THE NOTE EVENT, and the two things it must never carry. A note that went out is a state
// change of the fleet -- who said something to whom, in which lane -- and it is the one
// state change that is also somebody's private prose. These tests assert both halves: the
// line carries the id, the lane and the recipients, and it carries neither the body nor
// the subject nor the path that is minted from the subject (SPEC-LOGS.md Part 2).
//
// Nothing here opens a socket: the remote is this package's own bare fixture on disk.
//
// Seen red first: before events.go there was no line at all, and before the rule above was
// code the shape test passed while the leak tests failed.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type noteEvent struct {
	Level  string `json:"level"`
	Source string `json:"source"`
	Bench  string `json:"bench"`
	Verb   string `json:"verb"`
	Job    string `json:"job"`
	Event  string `json:"event"`
	Msg    string `json:"msg"`
	TS     string `json:"ts"`
	GUID   string `json:"guid"`
}

func readNoteEvents(t *testing.T, path string) []noteEvent {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the --log file was not written: %v", err)
	}
	var out []noteEvent
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e noteEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("a logged line is not one JSON object: %v\n%s", err, line)
		}
		out = append(out, e)
	}
	return out
}

func oneNoteEvent(t *testing.T, path, verb string) noteEvent {
	t.Helper()
	lines := readNoteEvents(t, path)
	var got []noteEvent
	for _, l := range lines {
		if l.Event == "note" {
			got = append(got, l)
		}
	}
	if len(got) != 1 {
		t.Fatalf("want exactly one note event from %s, got %d", verb, len(got))
	}
	if got[0].Source != "nova-bus" || got[0].Verb != verb || got[0].Bench != "hulk" {
		t.Fatalf("the line is missing its labels: source=%q verb=%q bench=%q", got[0].Source, got[0].Verb, got[0].Bench)
	}
	if got[0].TS == "" || got[0].GUID == "" {
		t.Fatalf("ts and guid are never absent: %+v", got[0])
	}
	return got[0]
}

// a-send-writes-one-note-event-with-the-id-the-lane-and-the-recipients.
func TestSendEmitsOneNoteEvent(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	eventLog := filepath.Join(t.TempDir(), "nova-events.log")
	draft := writeDraftFile(t, "From: Ada\nTo: Bo\nSubject: the key rotation is done\n\nthe body of the note\n")

	r := invoke(t, "", "send", "--bus", checkout, "--file", draft, "--remote", "origin",
		"--branch", "main", "--attempts", "3", "--no-push",
		"--bench", "hulk", "--log", eventLog).
		mustCode(t, 0).
		mustContain(t, "stdout", "SEND OK id=ada-")

	e := oneNoteEvent(t, eventLog, "send")
	id := field(t, r.stdout, "id=")
	if e.Job != id {
		t.Fatalf("the note id is the line's work item: job=%q, the run sent %q", e.Job, id)
	}
	for _, want := range []string{"id=" + id, "lane=from-ada", "to=1", "names=Bo", "pushed=false"} {
		if !strings.Contains(e.Msg, want) {
			t.Fatalf("the note event does not carry %q: %q", want, e.Msg)
		}
	}
	// THE TWO THINGS THAT MUST NEVER BE ON IT.
	if strings.Contains(e.Msg, "the body of the note") {
		t.Fatalf("a note's BODY reached the structured stream: %q", e.Msg)
	}
	if strings.Contains(e.Msg, "key rotation") {
		t.Fatalf("a note's SUBJECT reached the structured stream: %q", e.Msg)
	}
	// And the path, which carries the slug minted from the subject, is not on it either.
	if strings.Contains(e.Msg, "from-ada/") {
		t.Fatalf("the note's path (and so its slug, and so its subject) reached the stream: %q", e.Msg)
	}
}

// a-reply-writes-the-same-event-from-its-own-verb: same shape, verb="reply", so one query
// over `{source="nova-bus"} | json | event="note"` is everything that went out.
func TestReplyEmitsOneNoteEvent(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	eventLog := filepath.Join(t.TempDir(), "nova-events.log")
	draft := writeDraftFile(t, "Green on all three platforms.\n")

	invoke(t, "", "reply", "--bus", checkout, "--as", "Ada", "--re", "bo-abcdef012345",
		"--file", draft, "--remote", "origin", "--branch", "main", "--attempts", "3",
		"--bench", "hulk", "--log", eventLog).
		mustCode(t, 0).
		mustContain(t, "stdout", "REPLY OK id=ada-")

	e := oneNoteEvent(t, eventLog, "reply")
	for _, want := range []string{"lane=from-ada", "to=", "names=", "commit=", "pushed=true"} {
		if !strings.Contains(e.Msg, want) {
			t.Fatalf("the reply's note event does not carry %q: %q", want, e.Msg)
		}
	}
	if strings.Contains(e.Msg, "Green on all three platforms") {
		t.Fatalf("the reply's BODY reached the structured stream: %q", e.Msg)
	}
	if strings.Contains(e.Msg, "A question about the gate") {
		t.Fatalf("the original's SUBJECT reached the structured stream: %q", e.Msg)
	}
}

// A dry run writes no note, so it writes no note event: the stream says what happened on
// the bus, never what would have.
func TestSendDryRunEmitsNoNoteEvent(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	eventLog := filepath.Join(t.TempDir(), "nova-events.log")
	draft := writeDraftFile(t, "From: Ada\nTo: Bo\nSubject: dry run\n\nbody\n")

	invoke(t, "", "send", "--bus", checkout, "--file", draft, "--remote", "origin",
		"--branch", "main", "--attempts", "3", "--dry-run",
		"--bench", "hulk", "--log", eventLog).mustCode(t, 0)

	if raw, err := os.ReadFile(eventLog); err == nil && strings.Contains(string(raw), `"event":"note"`) {
		t.Fatalf("a dry run claimed a note went out:\n%s", raw)
	}
}

// A --log nobody can write is a refusal BEFORE the checkout is touched: a note that has
// gone out cannot be unsent by a logging failure discovered afterwards.
func TestSendRefusesALogItCannotOpen(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	draft := writeDraftFile(t, "From: Ada\nTo: Bo\nSubject: s\n\nbody\n")
	r := invoke(t, "", "send", "--bus", checkout, "--file", draft, "--remote", "origin",
		"--branch", "main", "--attempts", "3", "--no-push",
		"--log", filepath.Join(t.TempDir(), "no-such-directory", "nova-events.log")).
		mustCode(t, 2)
	if !strings.Contains(r.stderr, "--log") {
		t.Fatalf("the refusal names the flag: %q", r.stderr)
	}
	if strings.Contains(r.stdout, "SEND OK") {
		t.Fatalf("a refused send reported a note that never went: %q", r.stdout)
	}
}
