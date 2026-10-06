package friend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestAnUnknownHarnessIsNamed(t *testing.T) {
	t.Parallel()
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

// The claude adapter puts nothing into the session: each push is one line
// appended to the wake file in the state directory, which the session's own
// wait is watching; the file is made when absent and never truncated, and a
// missing directory is a refusal naming it.
func TestTheClaudeDelivererAppendsOneLineToTheWakeFile(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	d, err := NewDeliverer("claude", state, "bob", nil, nil)
	require.NoError(t, err)
	_, stub := d.(Stub)
	assert.False(t, stub, "claude has a deliver command")
	_, passive := d.(interface{ Passive() })
	assert.True(t, passive, "the daemon still takes nothing off the stream for claude")
	assert.Contains(t, Pushing(), "claude")

	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	c := d.(*ClaudeWake)
	var out strings.Builder
	c.Now, c.Out = func() time.Time { return at }, &out
	exit, err := d.Deliver(context.Background(), "CHECK n-1\nanswer it: /in/judgments/j-1.json\n")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	at = at.Add(time.Second)
	_, err = d.Deliver(context.Background(), "judgment j-2 /in/judgments/j-2.json")
	require.NoError(t, err)
	wake := ClaudeWakePath(state, "bob")
	b, err := os.ReadFile(wake)
	require.NoError(t, err)
	assert.Equal(t, "2026-10-06T12:00:00Z nova-friend: CHECK n-1 ⏎ answer it: /in/judgments/j-1.json\n"+
		"2026-10-06T12:00:01Z nova-friend: judgment j-2 /in/judgments/j-2.json\n", string(b))
	assert.Contains(t, out.String(), "one line appended to "+wake)

	// no session named: the friend of the state directory's status file
	require.NoError(t, WriteStatus(state, Status{Friend: "ada"}))
	d2, _ := NewDeliverer("claude", state, "", nil, nil)
	_, err = d2.Deliver(context.Background(), "x")
	require.NoError(t, err)
	assert.FileExists(t, ClaudeWakePath(state, "ada"))
	none := t.TempDir()
	d3, _ := NewDeliverer("claude", none, "", nil, nil)
	_, err = d3.Deliver(context.Background(), "x")
	assert.ErrorContains(t, err, "no friend named for the claude wake file in "+none)

	missing := filepath.Join(state, "gone")
	d4, _ := NewDeliverer("claude", missing, "bob", nil, nil)
	_, err = d4.Deliver(context.Background(), "x")
	assert.ErrorContains(t, err, "no state directory "+missing)
	assert.NoDirExists(t, missing, "a refusal makes nothing")
}
