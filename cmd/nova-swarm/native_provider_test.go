package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// A provider failure is not the card's (nativeprovider.go; tla/CardContract.tla,
// ProviderFailure): a run that ended with no result, and whose harness's own record says
// the provider failed it, is reported on a PROVIDER-FAIL line the member reads, with a
// reason; every other end stays as it was.

// providerRun is one card run through `native` with the fake harness: before, when set,
// is called with the slot's data home before the run begins (what an earlier run left).
func providerRun(t *testing.T, label, card string, before func(data string)) (stdout, stderr string) {
	t.Helper()
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	if before != nil {
		before(filepath.Join(slot, "data"))
	}
	cardPath := filepath.Join(root, label+".md")
	require.NoError(t, os.WriteFile(cardPath, []byte(card), 0o644))
	var out, errb bytes.Buffer
	run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--tokens", "unmetered", "--deadline", "30s", "--no-wall"},
		strings.NewReader(""), &out, &errb, time.Now())
	return out.String(), errb.String()
}

// olderRunsError is what a slot's log holds from a run before this one: it names an
// error the run under test never had, and is never the reason.
func olderRunsError(data string) {
	path := filepath.Join(data, "opencode", "log", "opencode.log")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte("timestamp=2030-01-01T00:00:00.000Z level=ERROR run=00000000 message=\"stream error\" error.error=\"an older run's rate limit\"\n"), 0o644)
}

// The run of the report: stream errors in the harness's log, a clean exit, no result. The
// reason is the first error line this run wrote, never an older run's; the verdict line is
// still INCOMPLETE.
func TestAProviderErrorInTheHarnessLogIsAProviderFailure(t *testing.T) {
	t.Parallel()
	out, errb := providerRun(t, "pf1", "FAKE-STREAM-ERROR\nFAKE-NORESULT\n", olderRunsError)
	assert.Contains(t, errb, "NATIVE PROVIDER-FAIL label=pf1 ")
	assert.Contains(t, errb, ` reason=provider: class=provider-5xx status=- msg=Streaming response failed: [internal_error] Stream error: h2 protocol error`)
	assert.NotContains(t, errb, "an older run's", "the log is read from where this run began")
	assert.NotContains(t, errb, "timestamp=", "the line is the message, not its timestamp")
	assert.Contains(t, out, "NATIVE INCOMPLETE ")
	assert.NotContains(t, out, "NATIVE OK", "the verdict line is unchanged: the run delivered nothing")
	assert.Equal(t, 1, strings.Count(errb, "NATIVE PROVIDER-FAIL"), "one line, the first error")
}

// The second trigger: a clean exit after a tool result, with no error line anywhere.
func TestARunThatEndsOnAToolResultIsAProviderFailure(t *testing.T) {
	t.Parallel()
	needsSQLite(t)
	_, errb := providerRun(t, "pf2", "FAKE-ENDS-ON-TOOL\nFAKE-NORESULT\n", nil)
	assert.Contains(t, errb, "NATIVE PROVIDER-FAIL label=pf2 ")
	assert.Contains(t, errb, " reason=provider: class=other status=- msg=ended without a final message")
}

// The session's own record of the failed message is the cause when it has one: the
// provider's status and words, the class they say, and no key-shaped value.
func TestTheSessionsRecordOfTheFailedMessageIsTheCause(t *testing.T) {
	t.Parallel()
	needsSQLite(t)
	_, errb := providerRun(t, "pf6", "FAKE-SESSION-ERROR\nFAKE-NORESULT\n", nil)
	assert.Contains(t, errb, "NATIVE PROVIDER-FAIL label=pf6 ")
	assert.Contains(t, errb, " reason=provider: class=out-of-credit status=402 msg=Insufficient credits. key [redacted]\n")
	assert.NotContains(t, errb, "abcdefghij", "a key-shaped value never reaches the line")
}

// A run that ends with a final assistant message and no result is the card's, as today.
func TestARunThatEndsWithAFinalMessageAndNoResultStaysNoResult(t *testing.T) {
	t.Parallel()
	needsSQLite(t)
	out, errb := providerRun(t, "pf3", "FAKE-ENDS-ON-FINAL\nFAKE-NORESULT\n", nil)
	assert.NotContains(t, errb, "PROVIDER-FAIL")
	assert.Contains(t, out, "NATIVE INCOMPLETE ")
	assert.NotContains(t, out, "NATIVE OK")
}

// A run with no error and no result, and no record of a session, stays no-result.
func TestARunWithNoErrorsAndNoResultStaysNoResult(t *testing.T) {
	t.Parallel()
	out, errb := providerRun(t, "pf4", "FAKE-NORESULT\n", olderRunsError)
	assert.NotContains(t, errb, "PROVIDER-FAIL")
	assert.Contains(t, out, "NATIVE INCOMPLETE ")
	assert.NotContains(t, out, "NATIVE OK")
}

// A provider error in the log beside a published result is not a failure: the card
// finished (and the transcript that stops on a tool is no failure either).
func TestAProviderErrorBesideAResultIsNotAFailure(t *testing.T) {
	t.Parallel()
	needsSQLite(t)
	out, errb := providerRun(t, "pf5", "FAKE-STREAM-ERROR\nFAKE-ENDS-ON-TOOL\nFAKE-RESULT ok\n", nil)
	assert.NotContains(t, errb, "PROVIDER-FAIL")
	assert.Contains(t, out, "NATIVE OK ")
}

// The log is read from the tail, at most providerLogTailBytes of what the run appended: an
// error older than the cap is not seen, one inside it is, and what an earlier run left
// before the offset is never read.
func TestTheHarnessLogIsReadFromItsTailAndFromTheRunsOffset(t *testing.T) {
	t.Parallel()
	data := t.TempDir()
	path := filepath.Join(data, "opencode", "log", "opencode.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	errLine := "timestamp=t level=ERROR run=r message=\"stream error\" error.error=\"HTTP 503 overloaded\"\n"
	filler := strings.Repeat("timestamp=t level=INFO run=r message=chatter\n", providerLogTailBytes/40)

	require.NoError(t, os.WriteFile(path, []byte(errLine+filler), 0o644))
	assert.Empty(t, providerLogError(data, 0, nil), "an error before the tail's cap is not read")

	require.NoError(t, os.WriteFile(path, []byte(filler+errLine), 0o644))
	assert.Equal(t, `message="stream error" error.error="HTTP 503 overloaded"`, providerLogError(data, 0, nil))

	require.NoError(t, os.WriteFile(path, []byte(errLine), 0o644))
	assert.Empty(t, providerLogError(data, int64(len(errLine)), nil), "an earlier run's line, before the offset, is not this run's")
	assert.Empty(t, providerLogError(t.TempDir(), 0, nil), "no log is no error")
}

// Each provider error the rule names is one, on an ERROR line only, and a line of
// another level that happens to say 500 is not.
func TestTheProviderErrorPatterns(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]bool{
		`level=ERROR message="stream error" error.error="x"`:              true,
		`level=ERROR error.error.type=server_error`:                       true,
		`level=ERROR error.error="AI_APICallError: Rate limit reached"`:   true,
		`level=ERROR message=failed statusCode=503`:                       true,
		`level=ERROR message=failed status 502 bad gateway`:               true,
		`level=ERROR message=failed error="HTTP 529 overloaded"`:          true,
		`level=ERROR message=failed error="file not found"`:               false,
		`level=INFO message="stream error" error.error.type=server_error`: false,
		`level=ERROR message=failed path=/tmp/x500/y`:                     false,
	} {
		data := t.TempDir()
		path := filepath.Join(data, "opencode", "log", "opencode.log")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("timestamp=t "+line+"\n"), 0o644))
		assert.Equal(t, want, providerLogError(data, 0, nil) != "", line)
	}
}

// The member reads the line: the kind is the provider's and the reason rides with it; a
// launch-grace 5xx line names no reason, and any other end names none.
func TestTheMemberReadsAProviderFailureLineAndItsReason(t *testing.T) {
	t.Parallel()
	log := "NATIVE PROVIDER-FAIL label=c1 wall=450.00s route=a\\x2fb reason=provider: message=\"stream error\" error=x y\n" +
		"NATIVE INCOMPLETE label=c1 job=j tmp=t rc=0 wall=none harness=ok budget=10/1000 why=no-result\n"
	assert.Equal(t, member.EndProvider, nativeEnd([]byte(log)))
	m := nativeProviderWhy.FindSubmatch([]byte(log))
	require.NotNil(t, m)
	assert.Equal(t, `provider: message="stream error" error=x y`, string(m[1]))
	assert.Nil(t, nativeProviderWhy.FindSubmatch([]byte("NATIVE PROVIDER-5XX label=c1 ref=- wall=3.00s route=x next=- avoid=x\n")))
	m = nativeProviderWhy.FindSubmatch([]byte("NATIVE PROVIDER-5XX label=c1 ref=- wall=3.00s route=x next=- avoid=x reason=provider: class=auth status=401 msg=bad key reason=words\n"))
	require.NotNil(t, m)
	assert.Equal(t, "provider: class=auth status=401 msg=bad key reason=words", string(m[1]), "the first reason= opens the reason")
}

// The 5xx hand-back keeps its own line as the reason, so the finish and the bound's judgment
// name what the provider answered and not the missing shape.
func TestTheHandbackLineIsItsOwnReason(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	close(done)
	line := "NATIVE PROVIDER-5XX label=c1 ref=err_fb35c63e wall=75.00s route=x/y next=- avoid=x/y"
	assert.Equal(t, "provider: "+strings.TrimPrefix(line, "NATIVE "), providerReason([]byte(line+"\n")))
	assert.Empty(t, providerReason([]byte("NATIVE OK label=c1 rc=0\n")))
	dir := t.TempDir()
	logPath := filepath.Join(dir, "c1.native.log")
	write(t, logPath, line+"\nNATIVE INCOMPLETE label=c1 job=j tmp=t rc=1 wall=none harness=ok budget=10/1000 why=no-result\n")
	c := &nativeChild{card: "c1", logPath: logPath, results: filepath.Join(dir, "results"), job: filepath.Join(dir, "job"), done: done}
	fin, why := member.Judge(c.Result(), member.Push{None: "no commit"})
	assert.Equal(t, member.FinishFailed, fin)
	assert.Equal(t, "provider failure: provider: PROVIDER-5XX label=c1 ref=err_fb35c63e wall=75.00s route=x/y next=- avoid=x/y", why)

	// the hand-back line that names its cause: the cause is the reason
	write(t, logPath, line+" reason=provider: class=unknown-model status=404 msg=No endpoints found for x/y.\n")
	c = &nativeChild{card: "c1", logPath: logPath, results: filepath.Join(dir, "results"), job: filepath.Join(dir, "job"), done: done}
	fin, why = member.Judge(c.Result(), member.Push{None: "no commit"})
	assert.Equal(t, member.FinishFailed, fin)
	assert.Equal(t, "provider failure: provider: class=unknown-model status=404 msg=No endpoints found for x/y.", why)
}

// From native's log to the member's finish: a child whose log carries the PROVIDER-FAIL
// line and whose job holds no result ends with the provider's kind and reason, which Judge
// puts first in the failed finish's reason; the same child with no such line is the
// shape's failure as before.
func TestAChildTheProviderFailedIsJudgedProviderFailure(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	close(done)
	for _, tc := range []struct{ name, log, want string }{
		{"the provider failed it", "NATIVE PROVIDER-FAIL label=c1 wall=450.00s route=x reason=provider: ended without a final message\n" +
			"NATIVE INCOMPLETE label=c1 job=j tmp=t rc=0 wall=none harness=ok budget=10/1000 why=no-result\n",
			"provider failure: provider: ended without a final message"},
		{"no line", "NATIVE INCOMPLETE label=c1 job=j tmp=t rc=0 wall=none harness=ok budget=10/1000 why=no-result\n", "no result: no RESULT.md shape"},
	} {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "c1.native.log")
		write(t, logPath, tc.log)
		c := &nativeChild{card: "c1", logPath: logPath, results: filepath.Join(dir, "results"), job: filepath.Join(dir, "job"), done: done}
		fin, why := member.Judge(c.Result(), member.Push{None: "the child committed nothing"})
		assert.Equal(t, member.FinishFailed, fin, tc.name)
		assert.Equal(t, tc.want, why, tc.name)
	}
}

// The harness's own UnknownError names no cause; with its logs printed on its stderr (the
// providers table's --print-logs --log-level ERROR) the error line it printed is the cause on
// the hand-back line, not the envelope's words, here a cause that is not the model (the
// model's refusal is #5042's start retry). Every start fails the same way, so the line ends
// with the starts tried.
func TestAnUnknownErrorCarriesTheErrorLineTheHarnessPrinted(t *testing.T) {
	t.Parallel()
	_, errb := providerRun(t, "pf7", "FAKE-UNKNOWN-ERROR\n", nil)
	assert.Contains(t, errb, "NATIVE PROVIDER-5XX label=pf7 ")
	assert.Contains(t, errb, " reason=provider: class=other status=- msg=ProviderInitError: the provider fake could not be loaded (harness starts tried: ")
	assert.NotContains(t, errb, "Unexpected server error", "the envelope's words are not the cause")
}

// The parent keeps the harness's last stderr lines itself (harnessErrTail): only the last
// captureTailLines non-empty lines, blank lines not counted, a line split across writes
// joined, and a last line with no newline kept.
func TestTheHarnessErrTailKeepsTheLastNonEmptyLines(t *testing.T) {
	t.Parallel()
	var tail harnessErrTail
	for i := range captureTailLines + 5 {
		_, _ = tail.Write([]byte("line " + strconv.Itoa(i) + "\n\n   \n")) // ignored: the tail's Write never fails
	}
	_, _ = tail.Write([]byte("split ")) // ignored: as above
	_, _ = tail.Write([]byte("across writes"))
	got := tail.Lines()
	require.Len(t, got, captureTailLines)
	assert.Equal(t, "line 6", got[0], "blank lines count for nothing")
	assert.Equal(t, "split across writes", got[len(got)-1])
}

// THE HARNESS'S PRINTED ERROR SHAPE is one named pattern (harnessPrintedErrorRE); its first
// row is the line a launch printed on its stderr, verbatim (opencode 1.18.20, 2026-10-01).
func TestTheHarnessPrintedErrorShape(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]bool{
		`timestamp=2026-10-01T20:03:01.112Z level=ERROR run=cfca2eb6 message="stream error" providerID=opencode modelID=kimi-k2.7-code session.id=ses_f06eff0b0ffel4NZaU2KiWmxHr small=false agent=build mode=primary error.error="AI_APICallError: Upstream request failed: Endpoint is unavailable."`: true,
		`timestamp=2030-01-02T03:04:05.000Z level=ERROR run=0a1b2c3d message="request failed" error="Model not found: x/y"`:                                                                                                                                                                             true,
		`timestamp=2030-01-02T03:04:05.000Z level=INFO run=0a1b2c3d message="started"`:                                                                                                                                                                                                                  false,
		`ERROR 2030-01-02T03:04:05 +2ms service=server message="Model not found: x/y"`:                                                                                                                                                                                                                  false,
		`timestamp=2026-10-01T17:51:42.310Z level=ERROR run=6bc9e82f message="stream error" providerID=x`:                                                                                                                                                                                               true,
		`ERROR: status 503 server_error`: false,
		`ERROR the test said rate limit`: false,
		`the log said: timestamp=2026-10-01T17:51:42.310Z level=ERROR run=6bc9e82f message="stream error"`: false,
		`level=ERROR message="stream error" error.error.type=server_error`:                                 false,
	} {
		assert.Equal(t, want, harnessPrintedErrorRE.MatchString(line), line)
	}
}

// A CARD CANNOT MAKE ITS OWN FAILURE THE PROVIDER'S. A child that writes a line in the
// harness's printed error shape, naming a 503, into its job's capture file, and a model whose
// last output quotes such a line, both end with no result and stay the card's: the parent
// reads only the harness's stderr, from its own copy.
func TestACardCannotSpoofAProviderFailure(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, card string }{
		{"a line written into the job's capture file", "FAKE-SPOOF-CAPTURE\nFAKE-NORESULT\n"},
		{"a line the model quotes on its output", "FAKE-QUOTE-ERROR\nFAKE-NORESULT\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, errb := providerRun(t, "spoof", tc.card, nil)
			assert.NotContains(t, errb, "PROVIDER-", "the card's own end stays its own")
			assert.Contains(t, out, "NATIVE INCOMPLETE ")
		})
	}
}

// The harness's printed error lines are read from the parent's tail of its stderr, first
// the provider's for the failure, last any for the cause; nothing outside the shape counts.
func TestPrintedErrorLinesAreReadFromTheParentsTail(t *testing.T) {
	t.Parallel()
	tail := []string{
		`ERROR: status 503 server_error`,
		`timestamp=2026-10-01T20:03:01.112Z level=ERROR run=cfca2eb6 message="stream error" providerID=opencode modelID=kimi-k2.7-code session.id=ses_f06eff0b0ffel4NZaU2KiWmxHr small=false agent=build mode=primary error.error="AI_APICallError: Upstream request failed: Endpoint is unavailable."`,
		`timestamp=2030-01-02T03:04:06.000Z level=ERROR run=0a1b2c3d message="request failed" error="Model not found: x/y"`,
	}
	assert.Equal(t, `message="stream error" providerID=opencode modelID=kimi-k2.7-code session.id=ses_f06eff0b0ffel4NZaU2KiWmxHr small=false agent=build mode=primary error.error="AI_APICallError: Upstream request failed: Endpoint is unavailable."`, providerLogError(t.TempDir(), 0, tail))
	assert.Equal(t, `message="request failed" error="Model not found: x/y"`, captureErrorLine(tail))
	assert.Empty(t, providerLogError(t.TempDir(), 0, tail[:1]), "a line outside the harness's shape is not its error")
}
