package friend

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeExec records every command and answers what the test scripted.
type fakeExec struct {
	calls   [][]string
	listing string
	exit    int
	out     string
}

func (f *fakeExec) run(_ context.Context, dir, name string, args []string, _ string) (string, int, error) {
	f.calls = append(f.calls, append([]string{dir, name}, args...))
	if len(args) > 0 && args[0] == "session" {
		return f.listing, 0, nil
	}
	return f.out, f.exit, nil
}

func TestOpenCodeDeliversIntoTheNewestSessionOfTheDirectory(t *testing.T) {
	t.Parallel()
	var turn strings.Builder
	fe := &fakeExec{listing: `[{"id":"old","directory":"/w/bob","updated":10},{"id":"new","directory":"/w/bob","updated":20},{"id":"other","directory":"/w/ada","updated":30}]`, exit: 0, out: "I ran the pong line.\n"}
	d, err := NewDeliverer("opencode", "/w/bob", "", fe.run, &turn)
	require.NoError(t, err)
	d.(*OpenCode).Look = nil // this case is the headless run, with no TUI probe
	exit, err := d.Deliver(context.Background(), "hello")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	require.Len(t, fe.calls, 2)
	assert.Equal(t, []string{"/w/bob", "opencode", "session", "list", "--format", "json"}, fe.calls[0])
	assert.Equal(t, []string{"/w/bob", "opencode", "run", "--session", "new", "--dir", "/w/bob", "hello"}, fe.calls[1], "the text is an argument, never a shell line")
	assert.Equal(t, "I ran the pong line.\n", turn.String(), "the turn's output goes to the daemon's record")
	assert.Equal(t, "ab\n[... 3 more bytes]", Head("abcde", 2))

	fe = &fakeExec{exit: 7}
	d, err = NewDeliverer("opencode", "/w/bob", "named", fe.run, &turn)
	require.NoError(t, err)
	d.(*OpenCode).Look = nil
	exit, err = d.Deliver(context.Background(), "x")
	require.NoError(t, err)
	assert.Equal(t, 7, exit, "the exit code is the harness's")
	assert.Equal(t, []string{"/w/bob", "opencode", "run", "--session", "named", "--dir", "/w/bob", "x"}, fe.calls[0], "a named session is not looked up")

	_, err = NewestSession(`[{"id":"a","directory":"/w/ada","updated":1}]`, "/w/bob")
	assert.ErrorContains(t, err, "no opencode session for /w/bob")
	_, err = NewestSession(`nope`, "/w/bob")
	assert.ErrorContains(t, err, "not a JSON list")
}

func TestOpenCodeDeliversIntoTheOpenTUISession(t *testing.T) {
	t.Parallel()
	const dir = "/w/bob"
	for _, tc := range []struct {
		name, command, cwd string
		listen             []string
		hold               bool
		url                string
	}{
		{name: "a normal tui listens on nothing", command: "opencode", cwd: dir, hold: true},
		{name: "a port flag is an attach url", command: "opencode --port 4096", cwd: dir, hold: true, url: "http://127.0.0.1:4096"},
		{name: "a listen address wins over a quiet argv", command: "opencode", cwd: dir, listen: []string{"127.0.0.1:18080"}, hold: true, url: "http://127.0.0.1:18080"},
		{name: "a wildcard listen is loopback", command: "opencode", cwd: dir, listen: []string{"*:4096"}, hold: true, url: "http://127.0.0.1:4096"},
		{name: "attach argv names the server", command: "opencode attach http://127.0.0.1:4096", cwd: dir, hold: true, url: "http://127.0.0.1:4096"},
		{name: "a headless run is not a tui", command: "opencode run --session s --dir " + dir + " hello", cwd: dir},
		{name: "a tui in another directory does not hold this one", command: "opencode", cwd: "/w/ada"},
		{name: "serve is not the open tui", command: "opencode serve --port 4096", cwd: dir, listen: []string{"127.0.0.1:4096"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			hold, url := openCodeHold(tc.command, tc.cwd, dir, tc.listen)
			assert.Equal(t, tc.hold, hold)
			assert.Equal(t, tc.url, url)
		})
	}

	t.Run("a discovered server is a run attached to it", func(t *testing.T) {
		t.Parallel()
		var turn strings.Builder
		fe := &fakeExec{exit: 0, out: "pong\n"}
		o := &OpenCode{Dir: dir, Session: "ses", Run: fe.run, Out: &turn, Look: func(context.Context, string) (bool, string, error) {
			return true, "http://127.0.0.1:4096", nil
		}}
		exit, err := o.Deliver(context.Background(), "hello")
		require.NoError(t, err)
		assert.Equal(t, 0, exit)
		require.Len(t, fe.calls, 1)
		assert.Equal(t, []string{dir, "opencode", "run", "--attach", "http://127.0.0.1:4096", "--session", "ses", "--dir", dir, "hello"}, fe.calls[0])
		assert.Contains(t, turn.String(), "turned: opencode run --attach http://127.0.0.1:4096 session ses")
		route, line, err := o.Route(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "attach", route)
		assert.Equal(t, "http://127.0.0.1:4096", line)
	})

	t.Run("a normal tui defers and does not run", func(t *testing.T) {
		t.Parallel()
		fe := &fakeExec{}
		o := &OpenCode{Dir: dir, Session: "ses", Run: fe.run, Look: func(context.Context, string) (bool, string, error) {
			return true, "", nil
		}}
		exit, err := o.Deliver(context.Background(), "hello")
		assert.Equal(t, 0, exit)
		var deferred Deferred
		require.ErrorAs(t, err, &deferred)
		assert.Contains(t, deferred.Reason, "listens on nothing")
		assert.Contains(t, deferred.Reason, "in-process")
		assert.Contains(t, deferred.Reason, OpenCodeBusLine)
		assert.Empty(t, fe.calls, "a defer writes nothing and starts no run")
		route, line, err := o.Route(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "defer", route)
		assert.Equal(t, OpenCodeBusLine, line)
	})

	t.Run("no tui resumes by run and says so", func(t *testing.T) {
		t.Parallel()
		var turn strings.Builder
		fe := &fakeExec{exit: 0, out: "done\n"}
		o := &OpenCode{Dir: dir, Session: "ses", Run: fe.run, Out: &turn, Look: func(context.Context, string) (bool, string, error) {
			return false, "", nil
		}}
		exit, err := o.Deliver(context.Background(), "hello")
		require.NoError(t, err)
		assert.Equal(t, 0, exit)
		require.Len(t, fe.calls, 1)
		assert.Equal(t, []string{dir, "opencode", "run", "--session", "ses", "--dir", dir, "hello"}, fe.calls[0])
		assert.Contains(t, turn.String(), "answered by run, not by the open chat")
		route, _, err := o.Route(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "run", route)
	})

	t.Run("a process table that cannot be read defers", func(t *testing.T) {
		t.Parallel()
		fe := &fakeExec{}
		o := &OpenCode{Dir: dir, Run: fe.run, Look: func(context.Context, string) (bool, string, error) {
			return false, "", errors.New("ps failed")
		}}
		_, err := o.Deliver(context.Background(), "hello")
		var deferred Deferred
		require.ErrorAs(t, err, &deferred)
		assert.Contains(t, deferred.Reason, "process table")
		assert.Empty(t, fe.calls)
		route, line, err := o.Route(context.Background())
		assert.Equal(t, "defer", route)
		assert.Equal(t, OpenCodeBusLine, line)
		assert.Error(t, err)
	})
}

func TestTheOtherHarnessesRefuseHonestlyAndAnUnknownOneIsNamed(t *testing.T) {
	t.Parallel()
	for _, h := range []string{"claude"} {
		t.Run(h, func(t *testing.T) {
			t.Parallel()
			d, err := NewDeliverer(h, "/w/bob", "", nil, nil)
			require.NoError(t, err)
			_, err = d.Deliver(context.Background(), "x")
			assert.EqualError(t, err, "no deliver command for "+h+" yet; run the session's blocking read: nova-bus recv --as <friend>")
		})
	}
	_, err := NewDeliverer("vim", "/w/bob", "", nil, nil)
	assert.EqualError(t, err, `"vim" is no harness; the harnesses are opencode, codex, claude, antigravity, dsh, gemini, grok, copilot, cursor, amp, goose, kiro, cline, aider, roo, windsurf, zed, warp`)
}
