package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The recorded refusal each harness left, and the source it was read from, so
// the four places a lane leaves its refusal are all read. The antigravity and
// gemini lines are their real refusals, quoted as the harness printed them.
func creditRefusals() []struct {
	harness string
	text    string
	source  string
} {
	return []struct {
		harness string
		text    string
		source  string
	}{
		{"claude", "Credit balance is too low", "stdout"},
		{"codex", `{"error":{"message":"You exceeded your current quota, please check your plan and billing details.","type":"insufficient_quota","param":null,"code":"insufficient_quota"}}`, "stderr"},
		{"opencode", `{"type":"error","error":{"type":"CreditsError","message":"No payment method"}}`, "log"},
		{"grok", `{"code":"Some resource has been exhausted","error":"Your team has either used all available credits or reached its monthly spending limit. To continue making API requests, please purchase more credits or raise your spending limit."}`, "report"},
		{"antigravity", "Insufficient AI Credits. Your credits will refresh 6:52 PM.", "log"},
		{"dsh", "Error: 402 Insufficient Balance", "stderr"},
		{"gemini", "Your prepayment credits are depleted.", "report"},
	}
}

// TestTheRefusalReaderSurfaceIsComplete: the reader surface the daemon's lane
// step drives names every function the reading is made of.
func TestTheRefusalReaderSurfaceIsComplete(t *testing.T) {
	t.Parallel()

	for i, f := range refusalReaders {
		require.NotNil(t, f, "reader %d", i)
	}
}

// TestEveryHarnessCreditRefusalMarksTheFriendDown: every harness's recorded
// credit refusal, read from each of the lane's stdout, stderr, harness log and
// REPORT.md, is a refusal of kind credits with the until now plus the retry,
// and the down line the daemon sends names it.
func TestEveryHarnessCreditRefusalMarksTheFriendDown(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 7, 16, 19, 0, 0, time.UTC)
	for _, tc := range creditRefusals() {
		t.Run(tc.harness, func(t *testing.T) {
			t.Parallel()

			var evidence LaneText
			switch tc.source {
			case "stdout":
				evidence.Stdout = tc.text
			case "stderr":
				evidence.Stderr = tc.text
			case "log":
				evidence.Log = tc.text
			case "report":
				evidence.Report = tc.text
			}
			require.Contains(t, evidence.Sources(), tc.text, "the text is read from the source the case names")

			r, ok := ReadRefusal(tc.harness, evidence, now)
			require.True(t, ok, "%s: %q is a refusal", tc.harness, tc.text)
			assert.Equal(t, tc.harness, r.Harness)
			assert.Equal(t, RefusalCredits, r.Kind, "a credit refusal is kind credits")
			assert.Equal(t, now.Add(CreditRetry), r.Until, "until is now plus the row's retry, default 24h")
			assert.NotEmpty(t, r.Reason)

			assert.Equal(t, "no credits: "+tc.harness+": "+r.Reason, DownReason(r))
			assert.Equal(t,
				[]string{"friend", "down", "ada", "--reason", "no credits: " + tc.harness + ": " + r.Reason, "--until", r.Until.UTC().Format(time.RFC3339)},
				DownArgv("ada", r))
		})
	}
}

// TestALaneErrorThatIsNotARefusalChangesNothing: a lane error no row knows is
// no refusal and calls neither Down nor Judge.
func TestALaneErrorThatIsNotARefusalChangesNothing(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 7, 16, 19, 0, 0, time.UTC)
	w := &RefusalWatch{Harness: "antigravity", Now: func() time.Time { return now }}
	down, judged := 0, 0
	w.Down = func(Refusal) { down++ }
	w.Judge = func(string) { judged++ }

	r, ok := w.Observe(LaneText{Stderr: "language server exited with code 1\n"})
	assert.False(t, ok, "an ordinary lane error is no refusal")
	assert.Equal(t, Refusal{}, r)
	assert.Zero(t, down, "no refusal, no down")
	assert.Zero(t, judged, "one lane is not alike")
}

// TestThreeAlikeLanesSurfaceOneJudgmentAndNoDown: three lanes ending with the
// same first error line surface one judgment and never a down; a fourth is no
// second judgment, and a different line starts the count again.
func TestThreeAlikeLanesSurfaceOneJudgmentAndNoDown(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 7, 16, 19, 0, 0, time.UTC)
	w := &RefusalWatch{Harness: "dsh", Now: func() time.Time { return now }}
	var downs []Refusal
	var judgments []string
	w.Down = func(r Refusal) { downs = append(downs, r) }
	w.Judge = func(text string) { judgments = append(judgments, text) }

	line := "dsh: unknown session id"
	for i := 0; i < AlikeLanes; i++ {
		_, ok := w.Observe(LaneText{Stderr: line + "\n"})
		require.False(t, ok, "an unknown line is no refusal")
	}
	require.Len(t, judgments, 1, "the third alike failure is one judgment")
	assert.Equal(t, "lanes failing alike: "+line, judgments[0])
	assert.Empty(t, downs, "an unknown wording is never a down")

	w.Observe(LaneText{Stderr: line + "\n"})
	assert.Len(t, judgments, 1, "the fourth alike failure is no second judgment")

	w.Observe(LaneText{Stderr: "another error\n"})
	w.Observe(LaneText{Stderr: "another error\n"})
	assert.Len(t, judgments, 1, "a different line starts the count again")
	w.Observe(LaneText{Stderr: "another error\n"})
	assert.Len(t, judgments, 2, "three of the new line is a new judgment")
}

// TestARefusalResetsTheAlikeCount: a known refusal between alike failures
// starts the count again, so the judgment names three in a row.
func TestARefusalResetsTheAlikeCount(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 7, 16, 19, 0, 0, time.UTC)
	w := &RefusalWatch{Harness: "opencode", Now: func() time.Time { return now }}
	judged, down := 0, 0
	w.Judge = func(string) { judged++ }
	w.Down = func(Refusal) { down++ }

	line := "opencode: something odd"
	w.Observe(LaneText{Stderr: line + "\n"})
	w.Observe(LaneText{Stderr: line + "\n"})
	r, ok := w.Observe(LaneText{Stderr: "Insufficient balance\n"})
	require.True(t, ok, "a known refusal is a refusal")
	assert.Equal(t, RefusalCredits, r.Kind)
	w.Observe(LaneText{Stderr: line + "\n"})
	w.Observe(LaneText{Stderr: line + "\n"})
	assert.Zero(t, judged, "two alike after a refusal are not three in a row")
	assert.Equal(t, 1, down)
}
