package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A lane whose only output is braces left nothing a person can read an error from: it is the
// watcher's read fault, said once, and counts toward no lane failing alike.
func TestOutputOfOnlyBracesIsAReadFaultNotALaneFailingAlike(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 14, 2, 40, 0, 0, time.UTC)
	w := &RefusalWatch{Harness: "antigravity", Now: func() time.Time { return now }}
	judged, unread := 0, []string{}
	w.Judge = func(string) { judged++ }
	w.Unreadable = func(first string) { unread = append(unread, first) }
	for i := 0; i < 3*AlikeLanes; i++ {
		_, ok := w.Observe(LaneText{Stdout: "{\n}\n"})
		require.False(t, ok)
	}
	assert.Zero(t, judged, "a bare brace is no error line to count")
	require.Len(t, unread, 1, "said once per run of them")
	assert.Contains(t, unread[0], "{", "the read fault names what was left")
	// a lane that said nothing at all is no read fault either
	w2 := &RefusalWatch{Harness: "antigravity", Now: func() time.Time { return now }, Unreadable: func(string) { t.Fatal("an empty lane is not unreadable") }}
	w2.Observe(LaneText{})
}

func TestFirstErrorLinePassesOverABareBraceAndErrorMessageJoinsTheRest(t *testing.T) {
	t.Parallel()
	lt := LaneText{Stdout: prettyJSONError}
	assert.Equal(t, `"error": "the language server is not running",`, FirstErrorLine(lt))
	assert.Equal(t, `{ "error": "the language server is not running", "code": 14 }`, ErrorMessage(lt))
	assert.True(t, Unreadable(LaneText{Stderr: "{"}))
	assert.False(t, Unreadable(lt))
	assert.False(t, Unreadable(LaneText{}), "silence is not unreadable")
	long := LaneText{Stderr: strings.Repeat("word ", 400)}
	assert.LessOrEqual(t, len(ErrorMessage(long)), ErrorMessageMax+len("..."), "the message is bounded")
	// the first source with a word wins: a stderr of braces falls through to stdout
	assert.Equal(t, "dsh: gone", FirstErrorLine(LaneText{Stderr: "{\n}", Stdout: "dsh: gone"}))
}

// Alike failures count only inside AlikeWithin of each other: three of one wording spread over
// a day are three stray failures, not three lanes in a row.
func TestAlikeFailuresSpreadOverADayAreNotAJudgment(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 14, 2, 40, 0, 0, time.UTC)
	w := &RefusalWatch{Harness: "dsh", Now: func() time.Time { return now }}
	judged := 0
	w.Judge = func(string) { judged++ }
	for i := 0; i < 5; i++ {
		w.Observe(LaneText{Stderr: "dsh: unknown session id\n"})
		now = now.Add(AlikeWithin + time.Minute)
	}
	assert.Zero(t, judged, "each is the first of a new run")
	for i := 0; i < AlikeLanes; i++ {
		w.Observe(LaneText{Stderr: "dsh: unknown session id\n"})
		now = now.Add(time.Minute)
	}
	assert.Equal(t, 1, judged, "three inside the bound are the judgment")
}
