package log

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The clock and guid are injected, so every case below is deterministic and none of
// them reads the real /proc or the real time.Now.
func fixedClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 9, 17, 16, 56, 3, 412000000, time.UTC) }
}

func fixedGUID() func() string { return func() string { return "a1b2c3" } }

func newLine() Line { return New(fixedClock(), fixedGUID(), "nova-pulse") }

// a-slog-line-carries-the-spec-fields: the JSON object's keys are the spec's table
// exactly -- no key missing, no key invented -- and the object is exactly one line.
func TestLineCarriesTheSpecFieldsAndNoMore(t *testing.T) {
	l := newLine()
	l.Verb = "launch"
	l.Event = "done"
	l.Msg = "one card launched"

	var buf bytes.Buffer
	if err := l.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw := buf.String()
	if strings.Count(raw, "\n") != 1 || !strings.HasSuffix(raw, "\n") {
		t.Fatalf("one JSON object per line, got %q", raw)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, raw)
	}
	want := map[string]any{
		"ts":     "2026-09-17T16:56:03.412Z",
		"level":  "INFO",
		"source": "nova-pulse",
		"bench":  "",
		"verb":   "launch",
		"job":    "",
		"card":   "",
		"pr":     float64(0),
		"run":    "",
		"slot":   "",
		"guid":   "a1b2c3",
		"event":  "done",
		"msg":    "one card launched",
		"dur_ms": float64(0),
		"err":    "",
	}
	for key, value := range want {
		gv, ok := got[key]
		if !ok {
			t.Errorf("field %q is missing from %s", key, raw)
			continue
		}
		if gv != value {
			t.Errorf("field %q = %v, want %v", key, gv, value)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("the object has %d fields, want the spec's %d: %s", len(got), len(want), raw)
	}
}

// an-absent-id-is-never-omitted: job, card, pr, run and slot are 0 or "" when they are
// not this scope's, and the key is still written, so `| json` never guesses.
func TestLineWritesAbsentIdsAsEmptyNotOmitted(t *testing.T) {
	var buf bytes.Buffer
	if err := newLine().Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw := buf.String()
	for _, pair := range []string{`"job":""`, `"card":""`, `"pr":0`, `"run":""`, `"slot":""`, `"bench":""`, `"err":""`} {
		if !strings.Contains(raw, pair) {
			t.Errorf("absent field %s is omitted from %s", pair, raw)
		}
	}
}

// a-msg-with-a-newline-is-escaped-through-oneline-escape: the sentence cannot add a second
// line, and the value a reader parses back is the oneline.Escape rendering -- ESCAPE and
// not FIELD, because msg is a sentence a person reads and a panel matches on, not one token
// a scanner splits a line into. See Line.Write.
func TestLineEscapesMsgThroughOnelineEscape(t *testing.T) {
	l := newLine()
	l.Event = "start"
	l.Msg = "line one\nline two\twith = and space"

	var buf bytes.Buffer
	if err := l.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw := buf.String()
	if strings.Count(raw, "\n") != 1 || !strings.HasSuffix(raw, "\n") {
		t.Fatalf("a newline in msg split the JSON line: %q", raw)
	}
	var got struct {
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, raw)
	}
	if want := oneline.Escape(l.Msg); got.Msg != want {
		t.Fatalf("msg = %q, want the oneline.Escape rendering %q", got.Msg, want)
	}
	if !strings.Contains(got.Msg, "with = and space") {
		t.Fatalf("the sentence lost its spaces or its equals sign: %q", got.Msg)
	}
	if strings.ContainsAny(got.Msg, "\n\t") {
		t.Fatalf("msg still holds a raw control character: %q", got.Msg)
	}
}

// an-err-with-a-newline-is-escaped-too: the same one-line promise covers the error slot.
func TestLineEscapesErr(t *testing.T) {
	l := newLine()
	l.Err = "boom\nsecond line"

	var buf bytes.Buffer
	if err := l.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw := buf.String()
	if strings.Count(raw, "\n") != 1 || !strings.HasSuffix(raw, "\n") {
		t.Fatalf("a newline in err split the JSON line: %q", raw)
	}
	var got struct {
		Err string `json:"err"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, raw)
	}
	if want := oneline.Escape(l.Err); got.Err != want {
		t.Fatalf("err = %q, want %q", got.Err, want)
	}
}

// The level the caller sets is the level the object carries, in the spec's uppercase.
func TestLineCarriesTheCallersLevel(t *testing.T) {
	l := newLine()
	l.Level = "WARN"
	l.Event = "retry"
	var buf bytes.Buffer
	if err := l.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	var got struct {
		Level string `json:"level"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Level != "WARN" {
		t.Fatalf("level = %q, want WARN", got.Level)
	}
}
