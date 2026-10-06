package friend

import (
	"context"
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
	exit, err = d.Deliver(context.Background(), "x")
	require.NoError(t, err)
	assert.Equal(t, 7, exit, "the exit code is the harness's")
	assert.Equal(t, []string{"/w/bob", "opencode", "run", "--session", "named", "--dir", "/w/bob", "x"}, fe.calls[0], "a named session is not looked up")

	_, err = NewestSession(`[{"id":"a","directory":"/w/ada","updated":1}]`, "/w/bob")
	assert.ErrorContains(t, err, "no opencode session for /w/bob")
	_, err = NewestSession(`nope`, "/w/bob")
	assert.ErrorContains(t, err, "not a JSON list")
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
	assert.EqualError(t, err, `"vim" is no harness; the harnesses are opencode, codex, claude, antigravity, dsh, gemini, grok, tmux, copilot, cursor, amp, goose, kiro, cline, aider, roo, windsurf, zed, warp`)
}

func TestClaudeIsPassiveAndInstallPrintsTheSessionsWait(t *testing.T) {
	t.Parallel()
	d, err := NewDeliverer("claude", "/work/bob", "", nil, nil)
	require.NoError(t, err)
	_, passive := d.(interface{ Passive() })
	assert.True(t, passive, "the daemon takes nothing off the stream for claude")

	wake := ClaudeWakePath("/state", "bob")
	assert.Equal(t, "/state/bob.wake", wake)
	line := ClaudeInstallLine("claude", "bob", wake)
	assert.Contains(t, line, "nova-bus wait --as bob --after <cursor> --wake-file "+wake)
	assert.Contains(t, line, "background task")
	for _, h := range []string{"grok", "opencode", "codex", "dsh", ""} {
		assert.Empty(t, ClaudeInstallLine(h, "bob", wake), h)
	}
}
