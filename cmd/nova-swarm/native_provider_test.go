package main

import (
	"bytes"
	"os"
	"path/filepath"
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
	assert.Contains(t, errb, ` reason=provider: message="stream error" providerID=fake modelID=fake-model`)
	assert.Contains(t, errb, "server_error")
	assert.Contains(t, errb, "h2 protocol error")
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
	assert.Contains(t, errb, " reason=provider: ended without a final message")
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
	assert.Empty(t, providerLogError(data, 0), "an error before the tail's cap is not read")

	require.NoError(t, os.WriteFile(path, []byte(filler+errLine), 0o644))
	assert.Equal(t, `message="stream error" error.error="HTTP 503 overloaded"`, providerLogError(data, 0))

	require.NoError(t, os.WriteFile(path, []byte(errLine), 0o644))
	assert.Empty(t, providerLogError(data, int64(len(errLine))), "an earlier run's line, before the offset, is not this run's")
	assert.Empty(t, providerLogError(t.TempDir(), 0), "no log is no error")
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
		assert.Equal(t, want, providerLogError(data, 0) != "", line)
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
		{"no line", "NATIVE INCOMPLETE label=c1 job=j tmp=t rc=0 wall=none harness=ok budget=10/1000 why=no-result\n", "no RESULT.md shape"},
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
