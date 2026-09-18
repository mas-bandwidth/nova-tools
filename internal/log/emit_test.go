package log

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The emitter is the ONE place a verb's structured line is built. These tests are the
// contract every verb that emits leans on: the fifteen fields, the four labels Alloy
// promotes, one line per call, nothing at all without a sink, and a secret never on the
// way out. They were seen red before internal/log/emit.go existed.

func fixedEmitter(w *bytes.Buffer, source, verb, bench string) *Emitter {
	e := NewEmitter(w, source, verb, bench)
	e.Clock = func() time.Time { return time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC) }
	e.GUID = func() string { return "guid-1" }
	return e
}

func decodeOne(t *testing.T, raw string) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want exactly one line, got %d:\n%s", len(lines), raw)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &obj); err != nil {
		t.Fatalf("line is not one JSON object: %v\n%s", err, lines[0])
	}
	return obj
}

func TestEmitterWritesOneLineWithTheFixedFields(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := fixedEmitter(&buf, "nova-merge", "batch", "hulk")
	l := e.Line("batch-member")
	l.PR = 1326
	l.Msg = "merged"
	e.Send(l)

	obj := decodeOne(t, buf.String())
	for _, k := range []string{"ts", "level", "source", "bench", "verb", "job", "card", "pr", "run", "slot", "guid", "event", "msg", "dur_ms", "err"} {
		if _, ok := obj[k]; !ok {
			t.Fatalf("field %q is missing from %v", k, obj)
		}
	}
	if len(obj) != 15 {
		t.Fatalf("want the fifteen fixed fields, got %d: %v", len(obj), obj)
	}
	if obj["source"] != "nova-merge" || obj["verb"] != "batch" || obj["bench"] != "hulk" {
		t.Fatalf("the labels Alloy promotes are wrong: %v", obj)
	}
	if obj["event"] != "batch-member" || obj["msg"] != "merged" {
		t.Fatalf("the event and its message are wrong: %v", obj)
	}
	if obj["pr"] != float64(1326) {
		t.Fatalf("pr is not the number given: %v", obj["pr"])
	}
	if obj["level"] != "INFO" {
		t.Fatalf("a state change that is not a warning or an error is INFO, got %v", obj["level"])
	}
	if obj["ts"] != "2026-09-18T12:00:00Z" {
		t.Fatalf("ts is not the injected clock's: %v", obj["ts"])
	}
	if obj["guid"] != "guid-1" {
		t.Fatalf("guid is not the injected source's: %v", obj["guid"])
	}
}

func TestEmitterWithNoSinkWritesNothing(t *testing.T) {
	t.Parallel()
	// A verb whose caller named no sink keeps its exact stdout and stderr: the
	// structured line is additive and never a replacement.
	var e *Emitter
	e.Emit("batch-start", "no sink, no line") // a nil emitter is a quiet one

	nilW := NewEmitter(nil, "nova-merge", "batch", "hulk")
	nilW.Emit("batch-start", "no writer, no line")
	nilW.Announce(EventDone, "done", time.Second, nil)
}

func TestAnnounceCarriesTheDurationAndTheError(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := fixedEmitter(&buf, "nova-merge", "queue", "vision")
	e.Announce(EventRefuse, "the lane could not be read", 1500*time.Millisecond, errors.New("no such lane"))

	obj := decodeOne(t, buf.String())
	if obj["event"] != EventRefuse {
		t.Fatalf("event is not the refusal: %v", obj)
	}
	if obj["level"] != "ERROR" {
		t.Fatalf("an announce carrying an error is ERROR, got %v", obj["level"])
	}
	if obj["dur_ms"] != float64(1500) {
		t.Fatalf("dur_ms is not the elapsed time: %v", obj["dur_ms"])
	}
	if obj["err"] != "no such lane" {
		t.Fatalf("err is not the error's text: %v", obj["err"])
	}
}

func TestAnnounceWithNoErrorIsInfo(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := fixedEmitter(&buf, "nova-pulse", "fill", "space")
	e.Announce(EventStart, "fill: one tick", 0, nil)
	obj := decodeOne(t, buf.String())
	if obj["level"] != "INFO" || obj["err"] != "" {
		t.Fatalf("a clean announce is INFO with no error: %v", obj)
	}
}

func TestEmitterRedactsASecretOnTheWayOut(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := fixedEmitter(&buf, "nova-merge", "react", "hulk")
	e.Emit("pr-checks-done", "enqueue with token ghp_0123456789abcdefghijABCDEFGHIJ0123456789")
	if strings.Contains(buf.String(), "ghp_0123456789abcdefghijABCDEFGHIJ0123456789") {
		t.Fatalf("a credential-shaped value reached the line: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "[redacted]") {
		t.Fatalf("the redaction did not happen: %s", buf.String())
	}
}

func TestEmitterNewlineInTheMessageCannotAddASecondLine(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := fixedEmitter(&buf, "nova-merge", "batch", "hulk")
	e.Emit("batch-verdict", "FAIL\nstep=test")
	decodeOne(t, buf.String()) // one line, or this fails
}

func TestSinkDefaultsToTheWriterGiven(t *testing.T) {
	t.Parallel()
	var fallback bytes.Buffer
	w, closer, err := Sink("", &fallback)
	if err != nil {
		t.Fatalf("an empty path is the fallback and never an error: %v", err)
	}
	if closer != nil {
		t.Fatalf("the fallback is the caller's writer and is never closed here")
	}
	if w != &fallback {
		t.Fatalf("an empty path must hand back the writer given")
	}
}

func TestSinkAppendsToTheFileNamed(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nova-events-merge.log")
	if err := os.WriteFile(path, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, closer, err := Sink(path, os.Stderr)
	if err != nil {
		t.Fatalf("a writable path is not a refusal: %v", err)
	}
	e := fixedEmitter(nil, "nova-merge", "queue", "hulk")
	e.W = w
	e.Emit("queue-depth", "running=1 waiting=2 unmergeable=0")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), "first\n") {
		t.Fatalf("the sink truncated the file it was told to append to: %q", string(raw))
	}
	obj := decodeOne(t, strings.TrimPrefix(string(raw), "first\n"))
	if obj["event"] != "queue-depth" {
		t.Fatalf("the appended line is not the event: %v", obj)
	}
}

func TestSinkRefusesAPathItCannotOpen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "no-such-directory", "nova-events.log")
	if _, _, err := Sink(path, os.Stderr); err == nil {
		t.Fatalf("a path that cannot be opened is a refusal, never a silent run with no log")
	}
}

// THE MESSAGE IS A SENTENCE, NOT A TOKEN. It is escaped with Escape and never with Field:
// Field escapes every space and every "=" too, which is right inside the human one-line
// grammar, where a scanner reads a value as one token, and wrong in a JSON object, where
// the quotes already delimit the value. The panels that read `running=1 waiting=2` out of a
// message could not match `running\x3d1\x20waiting\x3d2`, and no person could read it.
func TestTheMessageKeepsItsSpacesAndItsEqualsSigns(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := fixedEmitter(&buf, "nova-merge", "queue", "hulk")
	e.Emit("queue-depth", "running=1 waiting=2 unmergeable=0")
	obj := decodeOne(t, buf.String())
	if obj["msg"] != "running=1 waiting=2 unmergeable=0" {
		t.Fatalf("the message is not the sentence given: %q", obj["msg"])
	}
}

// An id reaches the line as it is, redacted and JSON-encoded and nothing else: slog's
// handler escapes whatever a card id holds, so a `| json | card="..."` query matches the id
// the stream carried and not a rendering of it.
func TestAnIdReachesTheLineAsItIs(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := fixedEmitter(&buf, "nova-work", "events", "hulk")
	l := e.Line("card-done")
	l.Card = "card-9382"
	e.Send(l)
	obj := decodeOne(t, buf.String())
	if obj["card"] != "card-9382" {
		t.Fatalf("the card id is not the id given: %q", obj["card"])
	}
}
