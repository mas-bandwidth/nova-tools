package log

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	t.Parallel()

	l := newLine()
	l.Verb = "launch"
	l.Event = "done"
	l.Msg = "one card launched"

	var buf bytes.Buffer
	err := l.Write(&buf)
	require.NoError(t, err, "Write: %v", err)
	raw := buf.String()
	require.False(t, strings.Count(raw, "\n") != 1 || !strings.HasSuffix(raw, "\n"), "one JSON object per line, got %q", raw)
	var got map[string]any
	err = json.Unmarshal([]byte(raw), &got)
	require.NoError(t, err, "not one JSON object: %v\n%s", err, raw)
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
		"msg":    oneline.Field("one card launched"),
		"dur_ms": float64(0),
		"err":    "",
	}
	for key, value := range want {
		gv, ok := got[key]
		if !assert.True(t, ok, "field %q is missing from %s", key, raw) {
			continue
		}
		assert.Equal(t, value, gv, "field %q = %v, want %v", key, gv, value)
	}
	require.Len(t, got, len(want), "the object has %d fields, want the spec's %d: %s", len(got), len(want), raw)
}

// an-absent-id-is-never-omitted: job, card, pr, run and slot are 0 or "" when they are
// not this scope's, and the key is still written, so `| json` never guesses.
func TestLineWritesAbsentIdsAsEmptyNotOmitted(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := newLine().Write(&buf)
	require.NoError(t, err, "Write: %v", err)
	raw := buf.String()
	for _, pair := range []string{`"job":""`, `"card":""`, `"pr":0`, `"run":""`, `"slot":""`, `"bench":""`, `"err":""`} {
		assert.Contains(t, raw, pair, "absent field %s is omitted from %s", pair, raw)
	}
}

// a-msg-with-a-newline-is-escaped-through-oneline-field: the sentence cannot add a second
// line, and the value a reader parses back is the oneline.Field rendering.
func TestLineEscapesMsgThroughOnelineField(t *testing.T) {
	t.Parallel()

	l := newLine()
	l.Event = "start"
	l.Msg = "line one\nline two\twith = and space"

	var buf bytes.Buffer
	err := l.Write(&buf)
	require.NoError(t, err, "Write: %v", err)
	raw := buf.String()
	require.False(t, strings.Count(raw, "\n") != 1 || !strings.HasSuffix(raw, "\n"), "a newline in msg split the JSON line: %q", raw)
	var got struct {
		Msg string `json:"msg"`
	}
	err = json.Unmarshal([]byte(raw), &got)
	require.NoError(t, err, "not one JSON object: %v\n%s", err, raw)
	want := oneline.Field(l.Msg)
	require.Equal(t, want, got.Msg, "msg = %q, want the oneline.Field rendering %q", got.Msg, want)
	require.False(t, strings.ContainsAny(got.Msg, "\n\t"), "msg still holds a raw control character: %q", got.Msg)
}

// an-err-with-a-newline-is-escaped-too: the same one-line promise covers the error slot.
func TestLineEscapesErr(t *testing.T) {
	t.Parallel()

	l := newLine()
	l.Err = "boom\nsecond line"

	var buf bytes.Buffer
	err := l.Write(&buf)
	require.NoError(t, err, "Write: %v", err)
	raw := buf.String()
	require.False(t, strings.Count(raw, "\n") != 1 || !strings.HasSuffix(raw, "\n"), "a newline in err split the JSON line: %q", raw)
	var got struct {
		Err string `json:"err"`
	}
	err = json.Unmarshal([]byte(raw), &got)
	require.NoError(t, err, "not one JSON object: %v\n%s", err, raw)
	want := oneline.Escape(l.Err)
	require.Equal(t, want, got.Err, "err = %q, want %q", got.Err, want)
}

// The level the caller sets is the level the object carries, in the spec's uppercase.
func TestLineCarriesTheCallersLevel(t *testing.T) {
	t.Parallel()

	l := newLine()
	l.Level = "WARN"
	l.Event = "retry"
	var buf bytes.Buffer
	err := l.Write(&buf)
	require.NoError(t, err, "Write: %v", err)
	var got struct {
		Level string `json:"level"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "WARN", got.Level, "level = %q, want WARN", got.Level)
}
