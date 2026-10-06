package friend

import (
	"context"
	"errors"
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
	assert.Equal(t, []string{"/w/bob", "opencode", "run", "--session", "new", "hello"}, fe.calls[1], "the text is an argument, never a shell line")
	assert.Equal(t, "I ran the pong line.\n", turn.String(), "the turn's output goes to the daemon's record")
	assert.Equal(t, "ab\n[... 3 more bytes]", Head("abcde", 2))

	fe = &fakeExec{exit: 7}
	d, err = NewDeliverer("opencode", "/w/bob", "named", fe.run, &turn)
	require.NoError(t, err)
	exit, err = d.Deliver(context.Background(), "x")
	require.NoError(t, err)
	assert.Equal(t, 7, exit, "the exit code is the harness's")
	assert.Equal(t, []string{"/w/bob", "opencode", "run", "--session", "named", "x"}, fe.calls[0], "a named session is not looked up")

	_, err = NewestSession(`[{"id":"a","directory":"/w/ada","updated":1}]`, "/w/bob")
	assert.ErrorContains(t, err, "no opencode session for /w/bob")
	_, err = NewestSession(`nope`, "/w/bob")
	assert.ErrorContains(t, err, "not a JSON list")
}

// A normal OpenCode TUI has no server to post into (opencode 1.18.30,
// packages/opencode/src/cli/cmd/tui.ts:234). The session here is a fake:
// a process listing, command, tab, cwd. Nothing is started.
func TestOpenCodeDeliversIntoTheOpenTUISession(t *testing.T) {
	t.Parallel()
	const dir = "/w/bob"
	for _, tc := range []struct {
		name, listing string
		holds         bool
	}{
		{name: "a normal tui holds the directory", listing: "opencode\t" + dir, holds: true},
		{name: "a port flag is still the tui and not a post", listing: "opencode --port 4096\t" + dir, holds: true},
		{name: "attach is still the tui and not a post", listing: "opencode attach http://127.0.0.1:4096\t" + dir, holds: true},
		{name: "a headless run is not the tui", listing: "opencode run --session ses hello\t" + dir, holds: false},
		{name: "a tui in another directory does not hold this one", listing: "opencode\t/w/ada", holds: false},
		{name: "serve is not the open tui", listing: "opencode serve --port 4096\t" + dir, holds: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.holds, openCodeTUIHeld(tc.listing, dir))
		})
	}

	t.Run("a tui that holds defers and the session reads the bus on its next turn", func(t *testing.T) {
		t.Parallel()
		fe := &fakeExec{}
		fake := OpenCodeTUI{Holds: openCodeTUIHeld("opencode\t"+dir, dir)}
		require.True(t, fake.Holds)
		o := &OpenCode{Dir: dir, Session: "ses", Run: fe.run, TUI: func(context.Context) (OpenCodeTUI, error) {
			return fake, nil
		}}
		exit, err := o.Deliver(context.Background(), "hello")
		assert.Equal(t, 0, exit)
		var deferred Deferred
		require.ErrorAs(t, err, &deferred)
		assert.Contains(t, deferred.Reason, "1.18.30")
		assert.Contains(t, deferred.Reason, "packages/opencode/src/cli/cmd/tui.ts:234")
		assert.Contains(t, deferred.Reason, "in-process")
		assert.Contains(t, deferred.Reason, OpenCodeBusLine)
		assert.NotContains(t, deferred.Reason, "\n")
		assert.Empty(t, fe.calls, "a defer writes nothing and starts no run")
		route, line, err := o.Route(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "defer", route)
		assert.Equal(t, OpenCodeBusLine, line)
	})

	t.Run("no tui resumes by run and says it is not the open chat", func(t *testing.T) {
		t.Parallel()
		var turn strings.Builder
		fe := &fakeExec{exit: 0, out: "done\n"}
		o := &OpenCode{Dir: dir, Session: "ses", Run: fe.run, Out: &turn, TUI: func(context.Context) (OpenCodeTUI, error) {
			return OpenCodeTUI{}, nil
		}}
		exit, err := o.Deliver(context.Background(), "hello")
		require.NoError(t, err)
		assert.Equal(t, 0, exit)
		require.Len(t, fe.calls, 1)
		assert.Equal(t, []string{dir, "opencode", "run", "--session", "ses", "hello"}, fe.calls[0], "no --dir, and no --attach")
		assert.Contains(t, turn.String(), "answered by run, not by the open chat")
		route, _, err := o.Route(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "run", route)
	})

	t.Run("a session that cannot be read defers and starts no run", func(t *testing.T) {
		t.Parallel()
		fe := &fakeExec{}
		o := &OpenCode{Dir: dir, Run: fe.run, TUI: func(context.Context) (OpenCodeTUI, error) {
			return OpenCodeTUI{}, errors.New("listing failed")
		}}
		_, err := o.Deliver(context.Background(), "hello")
		var deferred Deferred
		require.ErrorAs(t, err, &deferred)
		assert.Contains(t, deferred.Reason, "could not be read")
		assert.Contains(t, deferred.Reason, OpenCodeBusLine)
		assert.Empty(t, fe.calls)
		route, line, rerr := o.Route(context.Background())
		assert.Equal(t, "defer", route)
		assert.Equal(t, OpenCodeBusLine, line)
		assert.Error(t, rerr)
	})
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

// The finding of 2026-10-06: opencode v2.0.20's run has no --dir, and every
// delivery for the two OpenCode friends exited 1 ("Unrecognized flag: --dir in
// command opencode run"). The friend's directory is the process's working
// directory, never a flag, on every run the adapter makes: a batch turn, a
// lane's open and its turns, a read.
func TestOpenCodeDeliverRunsInTheDirWithoutADirFlag(t *testing.T) {
	t.Parallel()
	type call struct {
		dir  string
		args []string
	}
	var calls []call
	run := func(_ context.Context, dir, name string, args []string, _ string) (string, int, error) {
		calls = append(calls, call{dir, append([]string(nil), args...)})
		if args[0] == "session" {
			return `[{"id":"ses_1","directory":"/w/bob","updated":1},{"id":"ses_2","directory":"/w/bob","updated":2}]`, 0, nil
		}
		for _, a := range args {
			if a == "--dir" || strings.HasPrefix(a, "--dir=") {
				return "Unrecognized flag: --dir in command opencode run\n", 1, nil
			}
		}
		return "done\n", 0, nil
	}
	o := &OpenCode{Dir: "/w/bob", Run: run}
	exit, err := o.Deliver(context.Background(), "hello")
	require.NoError(t, err)
	assert.Zero(t, exit, "the batch turn runs")
	turn, err := o.DeliverTo(context.Background(), "ses_1", "a card")
	require.NoError(t, err)
	assert.Zero(t, turn.Exit, "a lane's turn runs")
	turn, err = o.RunRead(context.Background(), "prov/m", "a read")
	require.NoError(t, err)
	assert.Zero(t, turn.Exit, "a read runs")
	_, _ = o.OpenSession(context.Background(), "You are bob.") // ignored: the listing gains no session in this fake; the run's words are what is read
	require.NotEmpty(t, calls)
	for _, c := range calls {
		assert.Equal(t, "/w/bob", c.dir, "every run is in the friend's directory: %v", c.args)
		assert.NotContains(t, c.args, "--dir", "no run passes --dir: %v", c.args)
	}
	assert.Equal(t, []string{"run", "--session", "ses_2", "hello"}, calls[1].args)
}

// The adapter reads the installed opencode once at the daemon's start: a run
// verb whose help lacks a flag the adapter passes is one line naming the
// version; a help that lists none cannot tell and refuses nothing.
func TestOpenCodeCheckRunRefusesARunLackingAFlagItPasses(t *testing.T) {
	t.Parallel()
	help := "opencode run [message..]\n  --standalone --server --continue --session --fork --model --agent --format --file --title --thinking --auto\n"
	runner := func(help string) Exec {
		return func(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
			if args[0] == "--version" {
				return "2.0.20\n", 0, nil
			}
			return help, 0, nil
		}
	}
	assert.NoError(t, (&OpenCode{Dir: "/w/bob", Run: runner(help)}).CheckRun(context.Background()), "v2.0.20 takes every flag the adapter passes")
	err := (&OpenCode{Dir: "/w/bob", Run: runner(strings.Replace(help, "--session ", "", 1))}).CheckRun(context.Background())
	require.Error(t, err)
	assert.Equal(t, "opencode 2.0.20: its run verb has no --session (opencode run --help), which the adapter passes; no delivery can run until opencode takes it", err.Error())
	assert.NotContains(t, err.Error(), "\n", "one line")
	assert.NoError(t, (&OpenCode{Dir: "/w/bob", Run: runner("")}).CheckRun(context.Background()), "a help that lists no flag cannot tell")
}
