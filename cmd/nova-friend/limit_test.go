package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The recorded refusal each harness left, and the channel it was read from, so
// the harness's stderr and the runner's own log are both read. The antigravity
// and gemini lines are their real refusals, quoted as the harness printed them.
// The claude line is the harness's own refusal on stderr; the grok and gemini
// lines are read from the runner's log. A refusal is never read from the
// model's stdout or its REPORT.md (the test below pins that).
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
		{"claude", "Credit balance is too low", "stderr"},
		{"codex", `{"error":{"message":"You exceeded your current quota, please check your plan and billing details.","type":"insufficient_quota","param":null,"code":"insufficient_quota"}}`, "stderr"},
		{"opencode", `{"type":"error","error":{"type":"CreditsError","message":"No payment method"}}`, "log"},
		{"grok", `{"code":"Some resource has been exhausted","error":"Your team has either used all available credits or reached its monthly spending limit. To continue making API requests, please purchase more credits or raise your spending limit."}`, "log"},
		{"antigravity", "Insufficient AI Credits. Your credits will refresh 6:52 PM.", "log"},
		{"dsh", "Error: 402 Insufficient Balance", "stderr"},
		{"gemini", "Your prepayment credits are depleted.", "log"},
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
// credit refusal, read from the harness's stderr or the runner's own log, is a
// refusal of kind credits with the until now plus the row's credit_retry
// (1h by default), and the down line the daemon sends names it.
func TestEveryHarnessCreditRefusalMarksTheFriendDown(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 7, 16, 19, 0, 0, time.UTC)
	for _, tc := range creditRefusals() {
		t.Run(tc.harness, func(t *testing.T) {
			t.Parallel()

			var evidence LaneText
			switch tc.source {
			case "stderr":
				evidence.Stderr = tc.text
			case "log":
				evidence.Log = tc.text
			}
			require.Contains(t, evidence.Sources(), tc.text, "the text is read from the channel the case names")

			r, ok := ReadRefusal(tc.harness, evidence, 1, 0, now)
			require.True(t, ok, "%s: %q is a refusal", tc.harness, tc.text)
			assert.Equal(t, tc.harness, r.Harness)
			assert.Equal(t, RefusalCredits, r.Kind, "a credit refusal is kind credits")
			assert.Equal(t, now.Add(CreditRetry), r.Until, "until is now plus the row's retry, default 1h")
			assert.NotEmpty(t, r.Reason)

			assert.Equal(t, "no credits: "+tc.harness+": "+r.Reason, DownReason(r))
			assert.Equal(t,
				[]string{"friend", "down", "ada", "--reason", "no credits: " + tc.harness + ": " + r.Reason, "--until", r.Until.UTC().Format(time.RFC3339)},
				DownArgv("ada", r))
		})
	}
}

// TestAModelQuoteInStdoutIsNoRefusal: the model's stdout is never read, however
// plainly it carries a row's wording beside a provider status, even on a lane
// that exited non-zero. A brief, a report or a page the model read must not
// take a friend down.
func TestAModelQuoteInStdoutIsNoRefusal(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 7, 16, 19, 0, 0, time.UTC)
	for _, text := range []string{
		"Credit balance is too low\n",
		"402 payment required\n",
		"the page said: usage limit reached\n",
	} {
		r, ok := ReadRefusal("claude", LaneText{Stdout: text}, 1, 0, now)
		assert.False(t, ok, "stdout %q is the model's words, never a refusal", text)
		assert.Equal(t, Refusal{}, r)
	}
}

// TestAHarnessRefusalOnStderrMarksTheFriendDownWithTheRowsRetry: a provider
// 402 on the harness's stderr is a refusal, and its until is now plus the
// row's credit_retry, never a fixed 24h.
func TestAHarnessRefusalOnStderrMarksTheFriendDownWithTheRowsRetry(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 7, 16, 19, 0, 0, time.UTC)
	row := 45 * time.Minute
	r, ok := ReadRefusal("dsh", LaneText{Stderr: "Error: 402 Insufficient Balance\n"}, 1, row, now)
	require.True(t, ok, "a 402 on stderr is a refusal")
	assert.Equal(t, RefusalCredits, r.Kind)
	assert.Equal(t, now.Add(row), r.Until, "until is now plus the row's credit_retry, not 24h")
}

// TestTheWordsAloneAreNoRefusal: a wording with no evidence beside it -- no
// failed exit and no provider status or limit line -- is not a refusal, where
// the same line beside a 402 is. The match requires the harness's exit code or
// a provider status line, never the words alone.
func TestTheWordsAloneAreNoRefusal(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 7, 16, 19, 0, 0, time.UTC)
	r, ok := ReadRefusal("claude", LaneText{Stderr: "Credit balance is too low\n"}, 0, 0, now)
	assert.False(t, ok, "a successful lane's words alone are no refusal")
	assert.Equal(t, Refusal{}, r)

	r, ok = ReadRefusal("claude", LaneText{Stderr: "Error: 402 payment required\n"}, 0, 0, now)
	require.True(t, ok, "a provider status beside the words is a refusal even with no failed exit")
	assert.Equal(t, RefusalCredits, r.Kind)
}

// TestTheRefusalIsReadFromTheTailOnly: a refusal before the last RefusalTail
// bytes of a channel is not read, and the same refusal at the end is: the
// model's earlier text is far from the tail a harness says its refusal at.
func TestTheRefusalIsReadFromTheTailOnly(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 7, 16, 19, 0, 0, time.UTC)
	early := "Error: 402 Insufficient Balance\n" + strings.Repeat("model text\n", RefusalTail/5)
	r, ok := ReadRefusal("dsh", LaneText{Stderr: early}, 1, 0, now)
	assert.False(t, ok, "a refusal before the last %d bytes is not read", RefusalTail)

	late := strings.Repeat("model text\n", RefusalTail/5) + "Error: 402 Insufficient Balance\n"
	r, ok = ReadRefusal("dsh", LaneText{Stderr: late}, 1, 0, now)
	require.True(t, ok, "a refusal in the tail is read")
	assert.Equal(t, RefusalCredits, r.Kind)
}

// TestARefusalReasonRedactsTokens: the reason the daemon stores and sends
// carries no key or token: a long base64 run, an sk- key and a key=value are
// replaced by <redacted>.
func TestARefusalReasonRedactsTokens(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 7, 16, 19, 0, 0, time.UTC)
	const token = "sk-ant-api03-AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHHIIII"
	r, ok := ReadRefusal("dsh", LaneText{Stderr: "Error: 402 payment required key=" + token + "\n"}, 1, 0, now)
	require.True(t, ok, "the line is a refusal")
	assert.Contains(t, r.Reason, "<redacted>", "the token is redacted")
	assert.NotContains(t, r.Reason, token, "the token never rides into the reason")
	assert.NotContains(t, DownReason(r), token, "the down line carries no token")
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

	r, ok := w.Observe(LaneText{Stderr: "language server exited with code 1\n"}, 1)
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
		_, ok := w.Observe(LaneText{Stderr: line + "\n"}, 1)
		require.False(t, ok, "an unknown line is no refusal")
	}
	require.Len(t, judgments, 1, "the third alike failure is one judgment")
	assert.Equal(t, "lanes failing alike: "+line, judgments[0])
	assert.Empty(t, downs, "an unknown wording is never a down")

	w.Observe(LaneText{Stderr: line + "\n"}, 1)
	assert.Len(t, judgments, 1, "the fourth alike failure is no second judgment")

	w.Observe(LaneText{Stderr: "another error\n"}, 1)
	w.Observe(LaneText{Stderr: "another error\n"}, 1)
	assert.Len(t, judgments, 1, "a different line starts the count again")
	w.Observe(LaneText{Stderr: "another error\n"}, 1)
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
	w.Observe(LaneText{Stderr: line + "\n"}, 1)
	w.Observe(LaneText{Stderr: line + "\n"}, 1)
	r, ok := w.Observe(LaneText{Stderr: "Insufficient balance\n"}, 1)
	require.True(t, ok, "a known refusal is a refusal")
	assert.Equal(t, RefusalCredits, r.Kind)
	w.Observe(LaneText{Stderr: line + "\n"}, 1)
	w.Observe(LaneText{Stderr: line + "\n"}, 1)
	assert.Zero(t, judged, "two alike after a refusal are not three in a row")
	assert.Equal(t, 1, down)
}

// TestTheHarnessStderrIsCapturedApartFromTheModelsStdout: RealExec's answer joins
// the model's stdout and the harness's stderr, but a lane's stderr is caught on its
// own while the lane runs (friend.WithStderrCapture), so the credit-refusal reader
// takes the harness's channel and never the model's words, even on a lane that
// failed. The joined answer is read too, to show the words there would match: the
// separated capture is what keeps them from the reader.
func TestTheHarnessStderrIsCapturedApartFromTheModelsStdout(t *testing.T) {
	t.Parallel()

	lane := filepath.Join(t.TempDir(), "lane")
	script := "#!/bin/sh\n" +
		"printf 'Error: 402 payment required\\n'\n" +
		"printf 'Error: the language server exited\\n' >&2\n" +
		"exit 1\n"
	require.NoError(t, testbin.WriteExecutable(lane, []byte(script), 0o755))

	var harnessErr strings.Builder
	ctx := friend.WithStderrCapture(context.Background(), &harnessErr)
	out, exit, err := friend.RealExec(ctx, t.TempDir(), lane, nil, "")
	require.NoError(t, err, "a failed lane is no error to RealExec")
	require.Equal(t, 1, exit)
	require.Contains(t, out, "Error: 402 payment required", "the joined answer carries the model's stdout")
	require.Contains(t, out, "Error: the language server exited", "the joined answer carries the harness's stderr")

	assert.Contains(t, harnessErr.String(), "Error: the language server exited", "the capture carries the harness's stderr")
	assert.NotContains(t, harnessErr.String(), "Error: 402 payment required", "the capture never carries the model's stdout")

	now := time.Date(2026, 10, 7, 16, 19, 0, 0, time.UTC)
	_, ok := ReadRefusal("dsh", LaneText{Stderr: harnessErr.String()}, exit, 0, now)
	assert.False(t, ok, "the model's words on stdout are no refusal when the capture is read")
	_, ok = ReadRefusal("dsh", LaneText{Stderr: out}, exit, 0, now)
	assert.True(t, ok, "the same words in the joined answer would match; the capture is why they no longer reach the reader")
}

// TestModelStdoutPlusExitOneThroughWatchedPathDoesNotDownFriend: an integration regression
// through the actual fl.Watch / watched path: a command printing "Credit balance is too low"
// on stdout and exiting 1 does not down the friend (fl.Limited() == false, 0 downs).
// Conversely, when the refusal is on stderr with exit 1, the friend is marked down.
// A refusal's own wording in the model's stdout -- "Insufficient AI Credits. Your credits
// will refresh 6:52 PM." -- downs the friend at no exit either, with empty stderr: credits
// are read from the harness's stderr and the runner log alone.
func TestModelStdoutPlusExitOneThroughWatchedPathDoesNotDownFriend(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	stdoutScript := filepath.Join(tmp, "fail-stdout")
	require.NoError(t, testbin.WriteExecutable(stdoutScript, []byte("#!/bin/sh\nprintf 'Credit balance is too low\\n'\nexit 1\n"), 0o755))

	stderrScript := filepath.Join(tmp, "fail-stderr")
	require.NoError(t, testbin.WriteExecutable(stderrScript, []byte("#!/bin/sh\nprintf 'Credit balance is too low\\n' >&2\nexit 1\n"), 0o755))

	agFailScript := filepath.Join(tmp, "ag-fail-stdout")
	require.NoError(t, testbin.WriteExecutable(agFailScript, []byte("#!/bin/sh\nprintf 'Insufficient AI Credits. Your credits will refresh 6:52 PM.\\n'\nexit 1\n"), 0o755))

	agOKScript := filepath.Join(tmp, "ag-ok-stdout")
	require.NoError(t, testbin.WriteExecutable(agOKScript, []byte("#!/bin/sh\nprintf 'Insufficient AI Credits. Your credits will refresh 6:52 PM.\\n'\n"), 0o755))

	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

	// 1. Run through actual fl.Watch / watched path with failure on STDOUT:
	{
		downs := 0
		fl := &friend.Limits{
			Now:     func() time.Time { return now },
			Harness: "claude",
			Down:    func(time.Time, string) { downs++ },
		}
		rw := &RefusalWatch{Harness: "claude", Now: func() time.Time { return now }}
		rwDowns := 0
		rw.Down = func(r Refusal) {
			rwDowns++
			fl.Refuse(r.Kind, DownReason(r), r.Until)
		}

		limitWatch := fl.Watch(friend.RealExec)
		watched := func(ctx context.Context, d, prog string, args []string, stdin string) (string, int, error) {
			var harnessErr strings.Builder
			ctx = friend.WithStderrCapture(ctx, &harnessErr)
			out, exit, err := limitWatch(ctx, d, prog, args, stdin)
			if exit != 0 || err != nil {
				if _, _, alreadyHeld := fl.Limited(); !alreadyHeld {
					logDir := d
					if logDir == "" {
						logDir = tmp
					}
					rw.Observe(LaneText{Stderr: harnessErr.String(), Log: friend.RunnerLog(logDir)}, exit)
				}
			}
			return out, exit, err
		}

		out, exit, err := watched(context.Background(), tmp, stdoutScript, nil, "")
		require.NoError(t, err)
		assert.Equal(t, 1, exit)
		assert.Contains(t, out, "Credit balance is too low")
		_, _, limited := fl.Limited()
		assert.False(t, limited, "stdout with exit 1 must not down the friend through watched path")
		assert.Zero(t, downs, "no downs from Limits")
		assert.Zero(t, rwDowns, "no downs from RefusalWatch")
	}

	// 2. Run through actual fl.Watch / watched path with failure on STDERR:
	{
		downs := 0
		fl := &friend.Limits{
			Now:     func() time.Time { return now },
			Harness: "claude",
			Down:    func(time.Time, string) { downs++ },
		}
		rw := &RefusalWatch{Harness: "claude", Now: func() time.Time { return now }}
		rwDowns := 0
		rw.Down = func(r Refusal) {
			rwDowns++
			fl.Refuse(r.Kind, DownReason(r), r.Until)
		}

		limitWatch := fl.Watch(friend.RealExec)
		watched := func(ctx context.Context, d, prog string, args []string, stdin string) (string, int, error) {
			var harnessErr strings.Builder
			ctx = friend.WithStderrCapture(ctx, &harnessErr)
			out, exit, err := limitWatch(ctx, d, prog, args, stdin)
			if exit != 0 || err != nil {
				if _, _, alreadyHeld := fl.Limited(); !alreadyHeld {
					logDir := d
					if logDir == "" {
						logDir = tmp
					}
					rw.Observe(LaneText{Stderr: harnessErr.String(), Log: friend.RunnerLog(logDir)}, exit)
				}
			}
			return out, exit, err
		}

		out, exit, err := watched(context.Background(), tmp, stderrScript, nil, "")
		require.NoError(t, err)
		assert.Equal(t, 1, exit)
		assert.Contains(t, out, "Credit balance is too low")
		until, reason, limited := fl.Limited()
		assert.True(t, limited, "stderr with exit 1 must down the friend")
		assert.Equal(t, friend.KindCredits, fl.Kind())
		assert.Equal(t, 1, downs)
		assert.True(t, until.After(now))
		assert.Contains(t, reason, "Credit balance is too low")
	}

	// 3. Run through the same path with a real refusal's own wording on STDOUT, at
	// exit 1 with empty stderr and at exit 0: the model's stdout is never a credit
	// refusal, whatever the exit is.
	{
		downs := 0
		fl := &friend.Limits{
			Now:     func() time.Time { return now },
			Harness: "antigravity",
			Down:    func(time.Time, string) { downs++ },
		}
		rw := &RefusalWatch{Harness: "antigravity", Now: func() time.Time { return now }}
		rwDowns := 0
		rw.Down = func(r Refusal) {
			rwDowns++
			fl.Refuse(r.Kind, DownReason(r), r.Until)
		}

		limitWatch := fl.Watch(friend.RealExec)
		watched := func(ctx context.Context, d, prog string, args []string, stdin string) (string, int, error) {
			var harnessErr strings.Builder
			ctx = friend.WithStderrCapture(ctx, &harnessErr)
			out, exit, err := limitWatch(ctx, d, prog, args, stdin)
			if exit != 0 || err != nil {
				if _, _, alreadyHeld := fl.Limited(); !alreadyHeld {
					logDir := d
					if logDir == "" {
						logDir = tmp
					}
					rw.Observe(LaneText{Stderr: harnessErr.String(), Log: friend.RunnerLog(logDir)}, exit)
				}
			}
			return out, exit, err
		}

		const line = "Insufficient AI Credits. Your credits will refresh 6:52 PM."
		out, exit, err := watched(context.Background(), tmp, agFailScript, nil, "")
		require.NoError(t, err)
		require.Equal(t, 1, exit)
		require.Contains(t, out, line, "the joined output carries the model's stdout")
		_, _, limited := fl.Limited()
		assert.False(t, limited, "stdout with the refusal's own wording and exit 1, empty stderr, must not down the friend")
		assert.Zero(t, downs, "no downs from Limits")
		assert.Zero(t, rwDowns, "no downs from RefusalWatch")

		out, exit, err = watched(context.Background(), tmp, agOKScript, nil, "")
		require.NoError(t, err)
		require.Equal(t, 0, exit)
		require.Contains(t, out, line)
		_, _, limited = fl.Limited()
		assert.False(t, limited, "and at exit 0 it must not either")
		assert.Zero(t, downs)
		assert.Zero(t, rwDowns)
	}
}
