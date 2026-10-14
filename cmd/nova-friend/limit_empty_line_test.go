package main

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A lane's identity for the alike count must carry information: the first line
// that holds a letter or digit after JSON punctuation is trimmed, or the first
// string value of a JSON key message, error or detail. A failing body that
// prints as JSON starts with `{`; without this rule its first line is `{`, and
// three lanes failing for any three reasons are alike under a line that says
// nothing and lock every lane until a restart (docs/SPEC-FRIEND.md, the lanes).

// detector is one RefusalWatch with an injected clock and spies on what it
// hands the daemon, so a test steps it with no real time.
type detector struct {
	w      *RefusalWatch
	judged []string
	downs  int
}

func newDetector(now time.Time) *detector {
	d := &detector{}
	d.w = &RefusalWatch{Harness: "opencode", Now: func() time.Time { return now }}
	d.w.Judge = func(text string) { d.judged = append(d.judged, text) }
	d.w.Down = func(Refusal) { d.downs++ }
	return d
}

// TestTheAlikeLaneDetectorIgnoresALineThatSaysNothing: three lanes in a row
// fault only when the line that stands for each carries information; a JSON
// body is alike under its message, a body that says nothing is counted for
// nothing, different messages are not alike, and the same message three times
// faults once on a line capped at 200 characters.
func TestTheAlikeLaneDetectorIgnoresALineThatSaysNothing(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 14, 2, 40, 49, 0, time.UTC)

	t.Run("a JSON body is alike under its message, not its brace", func(t *testing.T) {
		t.Parallel()

		body := "{\n  \"error\": {\n    \"message\": \"rate limit\"\n  }\n}\n"
		d := newDetector(now)
		for i := 0; i < AlikeLanes; i++ {
			d.w.Observe(LaneText{Stdout: body})
		}
		require.Len(t, d.judged, 1, "three alike bodies are one judgment")
		assert.Contains(t, d.judged[0], "rate limit", "the message is the identity")
		assert.NotContains(t, d.judged[0], "failing alike: {", "a bare brace is never the identity")
	})

	t.Run("a body that says nothing is counted for nothing", func(t *testing.T) {
		t.Parallel()

		d := newDetector(now)
		for i := 0; i < AlikeLanes+2; i++ {
			d.w.Observe(LaneText{Stdout: "{\n"})
		}
		assert.Empty(t, d.judged, "a line that says nothing is no judgment and no fault")
	})

	t.Run("three different messages are not alike", func(t *testing.T) {
		t.Parallel()

		d := newDetector(now)
		for _, msg := range []string{"alpha failed", "beta failed", "gamma failed"} {
			d.w.Observe(LaneText{Stdout: msg + "\n"})
		}
		assert.Empty(t, d.judged, "a different line starts the count again")
	})

	t.Run("the same message three times faults once, capped at 200", func(t *testing.T) {
		t.Parallel()

		msg := "detail: " + strings.Repeat("x", 500)
		d := newDetector(now)
		for i := 0; i < AlikeLanes+1; i++ {
			d.w.Observe(LaneText{Stdout: msg + "\n"})
		}
		require.Len(t, d.judged, 1, "the fourth alike failure is no second judgment")
		line := strings.TrimPrefix(d.judged[0], AlikeJudgmentPrefix)
		assert.LessOrEqual(t, len(line), 200, "the identity keeps at most 200 characters")

		report := friend.LaneFaultReport(AlikeLanes, line)
		n, got, ok := friend.LaneFaultParts(report)
		require.True(t, ok, "the report round-trips through LaneFaultParts")
		assert.Equal(t, AlikeLanes, n)
		assert.Equal(t, line, got, "the capped line is the line the report carries")
	})
}
