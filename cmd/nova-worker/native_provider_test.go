package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// A provider failure is not the card's (nativeprovider.go; tla/CardContract.tla,
// ProviderFailure): a run that ended with no result, and whose harness's own record says
// the provider failed it, is reported on a PROVIDER-FAIL line the member reads, with a
// reason; every other end stays as it was.

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
		`level=ERROR message="stream error" error.error="x"`:                            true,
		`level=ERROR error.error.type=server_error`:                                     true,
		`level=ERROR error.error="AI_APICallError: Rate limit reached"`:                 true,
		`level=ERROR message=failed statusCode=503`:                                     true,
		`level=ERROR message=failed status 502 bad gateway`:                             true,
		`level=ERROR message=failed error="HTTP 529 overloaded"`:                        true,
		`level=ERROR message=failed error="file not found"`:                             false,
		`level=INFO message="stream error" error.error.type=server_error`:               false,
		`level=ERROR message=failed path=/tmp/x500/y`:                                   false,
		`level=ERROR error="Insufficient credits. Add more"`:                            true,
		`level=ERROR error.error="Upstream request failed: Insufficient account funds"`: true,
		`level=ERROR error="You exceeded your current quota"`:                           true,
		`level=ERROR message=failed statusCode=402`:                                     true,
		`level=ERROR message=failed statusCode=429`:                                     true,
		`level=ERROR message=failed status 404`:                                         false,
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

// providerOf takes the provider from a model id's first slash, and only when both sides
// of it are there: a model id with no provider, or none after it, names no harness.
func TestProviderOfNeedsBothSidesOfTheSlash(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		model, provider string
		ok              bool
	}{
		{"openai/gpt-5", "openai", true},
		{"a/b/c", "a", true},
		{"openai/", "", false},
		{"/gpt-5", "", false},
		{"gpt-5", "", false},
		{"", "", false},
	} {
		provider, ok := providerOf(c.model)
		assert.Equal(t, c.provider, provider, c.model)
		assert.Equal(t, c.ok, ok, c.model)
	}
}
