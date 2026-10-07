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

// The patterns a failed turn is read by (docs/SPEC-FRIEND.md, the loop: a
// broken session): HTTP 400, 401, 402 and 403 from the provider and an account
// out of credit are refusals of the session; an HTTP 429 is a rate limit, which
// defers; anything else is neither.
func TestAFailedTurnIsReadAsARefusalARateLimitOrNeither(t *testing.T) {
	t.Parallel()
	for out, want := range map[string]string{
		"Error: HTTP 400 Bad Request\n":                                 "provider HTTP 400: Error: HTTP 400 Bad Request",
		"request failed: status code 401\n":                             "provider HTTP 401: request failed: status code 401",
		`{"status": 402, "detail": "pay"}`:                              "provider HTTP 402",
		"Your credit balance is too low to access the API":              "provider refused: credit balance is too low",
		"Error: insufficient_quota: you exceeded your current quota":    "provider refused: insufficient_quota",
		`{"error":{"type":"permission_error","message":"not allowed"}}`: "permission_error: not allowed",
	} {
		reason, ok := ProviderRefusal(out)
		require.True(t, ok, "%q", out)
		assert.Contains(t, reason, want, "%q", out)
		_, limited := ProviderRateLimit(out)
		assert.False(t, limited, "%q", out)
	}
	for _, out := range []string{"Error: HTTP 429 Too Many Requests", `{"type":"rate_limit_error","message":"slow"}`, "429 too many requests"} {
		_, refused := ProviderRefusal(out)
		assert.False(t, refused, "%q", out)
		reason, limited := ProviderRateLimit(out)
		assert.True(t, limited, "%q", out)
		assert.NotEmpty(t, reason)
	}
	for _, out := range []string{"Error: HTTP 500 Internal Server Error", "exit 1: no such file", `{"type":"overloaded_error"}`, ""} {
		_, refused := ProviderRefusal(out)
		_, limited := ProviderRateLimit(out)
		assert.False(t, refused || limited, "%q", out)
	}
	exit, err := refused("ses_x", "Error: HTTP 429 Too Many Requests", 1, nil)
	assert.Equal(t, 1, exit)
	var deferred Deferred
	assert.ErrorAs(t, err, &deferred, "a rate limit is answered as a deferral")
}
