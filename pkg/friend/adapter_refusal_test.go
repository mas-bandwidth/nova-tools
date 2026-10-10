package friend

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// freddyRefusal is what Freddy's OpenCode session printed on every turn from
// 11:22 AM ET on 2026-10-04 (the launchd log, measured).
const freddyRefusal = "\x1b[0m\n> build · mercury-2.5\n\x1b[0m\n\x1b[91m\x1b[1mError: \x1b[0m{\"message\":\"The request could not be processed due to an unknown issue with your input. Please review your request and try again.\",\"type\":\"invalid_request_error\",\"param\":null,\"code\":\"invalid_request_error\"}\n"

func TestAProvidersRefusalIsReadFromTheTurnsOutput(t *testing.T) {
	t.Parallel()
	reason, ok := ProviderRefusal(freddyRefusal)
	require.True(t, ok)
	assert.Equal(t, "invalid_request_error: The request could not be processed due to an unknown issue with your input. Please review your request and try again.", reason)
	reason, ok = ProviderRefusal(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	require.True(t, ok)
	assert.Equal(t, "authentication_error: invalid x-api-key", reason)
	for _, passing := range []string{`{"type":"rate_limit_error","message":"slow down"}`, `{"type":"overloaded_error"}`, `{"type":"api_error"}`, "exit 1: no such file\n", ""} {
		_, ok := ProviderRefusal(passing)
		assert.False(t, ok, "a transient error, or none, is no refusal of the session: %q", passing)
	}
}

// Each adapter that sees the turn's output says a provider refusal as
// ProviderRefused with the session it delivered into; any other failure
// stays the exit it was.
func TestTheAdaptersSayAProvidersRefusalWithTheSession(t *testing.T) {
	t.Parallel()
	refusing := func(context.Context, string, string, []string, string) (string, int, error) {
		return freddyRefusal, 1, nil
	}
	for _, c := range []struct {
		name    string
		d       Deliverer
		session string
	}{
		{"opencode", &OpenCode{Dir: "/w/bob", Session: "ses_x", Run: refusing}, "ses_x"},
		{"dsh", &DSH{Dir: "/w/bob", Session: "session-x", Run: refusing, Program: "dsh"}, "session-x"},
		{"gemini", &Gemini{Dir: "/w/bob", Session: "", Run: refusing}, "latest"},
		{"codex", &Codex{Dir: "/w/bob", Session: "thread-x", Run: refusing, Home: "/nonexistent", Held: func(string) bool { return false }}, "thread-x"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			exit, err := c.d.Deliver(context.Background(), "x")
			assert.Equal(t, 1, exit)
			var refused ProviderRefused
			require.True(t, errors.As(err, &refused), "%v", err)
			assert.Equal(t, c.session, refused.Session)
			assert.Contains(t, refused.Reason, "invalid_request_error")
		})
	}
	fe := &fakeExec{exit: 1, out: "Error: no such session\n"}
	exit, err := (&OpenCode{Dir: "/w/bob", Session: "ses_x", Run: fe.run}).Deliver(context.Background(), "x")
	assert.Equal(t, 1, exit)
	assert.NoError(t, err, "a failure that is no provider's refusal stays an exit code")
}

// The command's every write is said to the watch the daemon put in the
// context, stdout and stderr alike: that is how a working turn is told from
// a silent one.
func TestEveryWriteOfTheCommandIsSaidToTheWatch(t *testing.T) {
	t.Parallel()
	n := 0
	ctx := WithOutputSeen(context.Background(), func() { n++ })
	seen, _ := ctx.Value(outputKey{}).(func())
	w := &seenWriter{seen: seen}
	_, _ = w.Write([]byte("a"))
	_, _ = w.Write(nil)
	_, _ = w.Write([]byte("b"))
	assert.Equal(t, 2, n, "an empty write is no output")
	assert.Equal(t, "ab", w.b.String())
	_, _ = (&seenWriter{}).Write([]byte("no watch")) // a context without one is fine
}
