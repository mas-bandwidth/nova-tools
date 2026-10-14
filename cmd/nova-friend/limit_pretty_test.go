package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// prettyJSONError is a harness's error as it prints it when it indents: a bare brace opens it.
const prettyJSONError = "{\n  \"error\": \"the language server is not running\",\n  \"code\": 14\n}\n"

// Three failing commands whose output opens with a bare brace are not three lanes failing
// alike on "{": the judgment names the error, whole (emma's lanes locked on `refusal="{"` on
// 2026-10-14 and the seat was told "lanes failing alike: {"). Only the surface from before the
// change is used here, so this test runs on the code before it and fails there.
func TestThreePrettyJSONErrorsAreJudgedByTheirMessageNotTheirBrace(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 14, 2, 40, 0, 0, time.UTC)
	w := &RefusalWatch{Harness: "antigravity", Now: func() time.Time { return now }}
	var judgments []string
	w.Judge = func(text string) { judgments = append(judgments, text) }
	for i := 0; i < AlikeLanes; i++ {
		_, ok := w.Observe(LaneText{Stdout: prettyJSONError})
		require.False(t, ok)
	}
	require.Len(t, judgments, 1, "three alike errors are one judgment")
	assert.NotEqual(t, "lanes failing alike: {", judgments[0], "the judgment is not the brace")
	assert.Contains(t, judgments[0], "the language server is not running", "the whole message is kept")
	assert.Contains(t, judgments[0], `"code": 14`, "the lines are joined, none dropped")
	assert.NotContains(t, judgments[0], "\n", "one line")
}
