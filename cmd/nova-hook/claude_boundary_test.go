package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// claudeHookRun runs the installed boundary command once with stdin as the
// hook's input and env over the rig's environment.
func claudeHookRun(t *testing.T, state, settings, stdin string, env map[string]string) testkit.Result {
	t.Helper()
	r := newRig(t)
	w := r.world()
	base := w.getenv
	w.getenv = func(k string) string {
		if v, ok := env[k]; ok {
			return v
		}
		return base(k)
	}
	return testkit.Main(func(args []string, in io.Reader, stdout, stderr io.Writer) int {
		return run(args, in, stdout, stderr, w)
	}).RunIn(stdin, "hook", "--as", "bob", "--role", "friend", "--harness", "claude", "--claude-state", state, "--claude-settings", settings, "--claude-boundary")
}

// The installed Stop hook advances the generation for the session's own turn,
// and leaves it alone for a headless claude child (a friend lane's card, read or
// harness proof) whose CLAUDE_CONFIG_DIR is the directory the hook went in:
// 2026-10-16, the daemon's proof advanced a session's generation every minute
// (tla/ClaudeHookNotice.tla, ChildNoBoundary).
func TestTheClaudeBoundarySkipsAHeadlessChild(t *testing.T) {
	t.Parallel()
	state, settings := t.TempDir(), t.TempDir()
	l, err := notice.ClaudeWatch(state) // a watcher is armed: the stop ends the turn
	require.NoError(t, err)
	defer func() { require.NoError(t, l.Unlock()) }()
	gen := func() int {
		n, err := notice.ClaudeGeneration(state)
		require.NoError(t, err)
		return n
	}

	out := claudeHookRun(t, state, settings, "", nil)
	require.Zero(t, out.Code, out.Stdout+out.Stderr)
	assert.Contains(t, out.Stdout, "state=boundary generation=1")
	assert.Equal(t, 1, gen())

	child := map[string]string{notice.ClaudeChildEnv: "1"}
	out = claudeHookRun(t, state, settings, `{"hook_event_name":"Stop"}`, child)
	require.Zero(t, out.Code, out.Stdout+out.Stderr)
	assert.Contains(t, out.Stdout, "state=boundary-skipped generation=1")
	assert.Equal(t, 1, gen(), "a child's Stop leaves the session's generation alone")
	out = claudeHookRun(t, state, settings, `{"hook_event_name":"SessionStart"}`, child)
	require.Zero(t, out.Code)
	assert.Empty(t, out.Stdout, "a child's other events are no event of the session's")

	out = claudeHookRun(t, state, settings, `{"hook_event_name":"Stop","stop_hook_active":false}`, nil)
	require.Zero(t, out.Code, out.Stdout+out.Stderr)
	assert.Equal(t, 2, gen())
}

// Each event answers as Claude Code reads it: a Stop that blocks is one JSON
// decision; SessionStart is one JSON additionalContext naming the watch
// script; PostToolUse with nothing pending prints nothing.
func TestTheClaudeHookAnswersEachEventInClaudesForm(t *testing.T) {
	t.Parallel()
	state, settings := t.TempDir(), t.TempDir()

	out := claudeHookRun(t, state, settings, `{"hook_event_name":"Stop"}`, nil)
	require.Zero(t, out.Code, out.Stdout+out.Stderr)
	var stop struct{ Decision, Reason string }
	require.NoError(t, json.Unmarshal([]byte(out.Stdout), &stop), out.Stdout)
	assert.Equal(t, "block", stop.Decision, "no watcher is armed")
	assert.Contains(t, stop.Reason, filepath.Join(settings, "nova-hook-claude-watch.sh"))

	out = claudeHookRun(t, state, settings, `{"hook_event_name":"SessionStart","source":"compact"}`, nil)
	require.Zero(t, out.Code, out.Stdout+out.Stderr)
	var start struct {
		Out struct {
			Event, Context string
		} `json:"hookSpecificOutput"`
	}
	var raw map[string]map[string]string
	require.NoError(t, json.Unmarshal([]byte(out.Stdout), &raw), out.Stdout)
	start.Out.Event, start.Out.Context = raw["hookSpecificOutput"]["hookEventName"], raw["hookSpecificOutput"]["additionalContext"]
	assert.Equal(t, "SessionStart", start.Out.Event)
	assert.Contains(t, start.Out.Context, "Monitor tool")

	out = claudeHookRun(t, state, settings, `{"hook_event_name":"PostToolUse"}`, nil)
	require.Zero(t, out.Code, out.Stdout+out.Stderr)
	assert.Empty(t, out.Stdout)

	_, err := os.Stat(filepath.Join(state, "generation"))
	assert.ErrorIs(t, err, os.ErrNotExist, "no event here was a turn boundary")
}

// The watcher follows the service log from its end, as tail -F: whole lines
// only, a truncated or replaced file read from its start, and only the HOOK
// lines a model acts on.
func TestTheClaudeWatcherFollowsTheLog(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := filepath.Join(dir, "bob.log")
	require.NoError(t, os.WriteFile(log, []byte("HOOK PUSH old\n"), 0o600))
	tl := &claudeTail{path: log}
	require.NoError(t, tl.start())
	got, err := tl.next()
	require.NoError(t, err)
	assert.Empty(t, got, "the log is read from its end")

	f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteString("HOOK PUSH a\nHOOK NOTE n\nHOOK STATE s\npart")
	require.NoError(t, err)
	got, err = tl.next()
	require.NoError(t, err)
	assert.Equal(t, []string{"HOOK PUSH a", "HOOK NOTE n", "HOOK STATE s"}, got, "a partial line waits")
	_, err = f.WriteString("ial\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	got, err = tl.next()
	require.NoError(t, err)
	assert.Equal(t, []string{"partial"}, got)

	require.NoError(t, os.WriteFile(log+".new", []byte("HOOK PUSH b\n"), 0o600))
	require.NoError(t, os.Rename(log+".new", log))
	got, err = tl.next()
	require.NoError(t, err)
	assert.Equal(t, []string{"HOOK PUSH b"}, got, "a replaced log is read from its start")

	assert.True(t, claudeWatched("HOOK PUSH push=claude id=n1"))
	assert.True(t, claudeWatched("HOOK STATE state=live"))
	assert.False(t, claudeWatched("HOOK NOTE one notice"))
	assert.False(t, claudeWatched("RUN something"))
}

// A hook's error never blocks: exit 1, which Claude Code shows and passes,
// never a Refuse's exit 2, which would block a Stop for as long as the error
// stands (a state directory gone, a corrupt notice, input that is not JSON).
func TestAClaudeHookErrorNeverBlocks(t *testing.T) {
	t.Parallel()
	state, settings := t.TempDir(), t.TempDir()
	gone := filepath.Join(state, "gone")
	out := claudeHookRun(t, gone, settings, `{"hook_event_name":"Stop"}`, nil)
	assert.Equal(t, 1, out.Code, out.Stdout+out.Stderr)

	out = claudeHookRun(t, state, settings, "not json", nil)
	assert.Equal(t, 1, out.Code, out.Stdout+out.Stderr)

	// another route's environment, inherited from the shell Claude Code runs in
	out = claudeHookRun(t, state, settings, `{"hook_event_name":"Stop"}`, map[string]string{"NOVA_HOOK_ANTIGRAVITY_DIR": "/elsewhere"})
	assert.Equal(t, 1, out.Code, out.Stdout+out.Stderr)
	out = claudeHookRun(t, state, settings, `{"hook_event_name":"Stop"}`, map[string]string{"NOVA_HOOK_CODEX_SINCE": "not-a-time"})
	assert.Equal(t, 1, out.Code, out.Stdout+out.Stderr)

	bad := filepath.Join(state, "bad.json")
	require.NoError(t, os.WriteFile(bad, []byte("{"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(state, "pending"), []byte(bad+"\n"), 0o600))
	out = claudeHookRun(t, state, settings, `{"hook_event_name":"Stop"}`, nil)
	assert.Equal(t, 1, out.Code, out.Stdout+out.Stderr)
	assert.NotContains(t, out.Stdout, `"block"`)
}

// cancelOn cancels when a write carries word: the watcher's first line, with
// no clock in the test.
type cancelOn struct {
	mu     sync.Mutex
	b      strings.Builder
	word   string
	cancel context.CancelFunc
}

func (w *cancelOn) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.b.Write(p)
	if strings.Contains(w.b.String(), w.word) {
		w.cancel()
	}
	return len(p), nil
}

// The watcher holds the watch lock while it runs, refuses a second watcher,
// and releases the lock when its context ends.
func TestTheClaudeWatcherHoldsTheLockWhileItRuns(t *testing.T) {
	t.Parallel()
	state, settings := t.TempDir(), t.TempDir()
	log := filepath.Join(t.TempDir(), "bob.log")
	r := newRig(t)
	w := r.world()
	args := []string{"hook", "--as", "bob", "--role", "friend", "--harness", "claude", "--claude-state", state, "--claude-settings", settings, "--claude-watch", "--claude-log", log}

	held, err := notice.ClaudeWatch(state)
	require.NoError(t, err)
	var out, errs strings.Builder
	code := hookTool(w).RunContext(context.Background(), args, strings.NewReader(""), &out, &errs)
	assert.Equal(t, 2, code, "a second watcher is refused: "+out.String())
	assert.Contains(t, out.String()+errs.String(), "another watcher already holds")
	require.NoError(t, held.Unlock())

	pending := filepath.Join(state, "n1.json")
	require.NoError(t, os.WriteFile(pending, []byte(`{"local_receiver":{"identity":"bob"},"messages":1}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(state, "pending"), []byte(pending+"\n"), 0o600))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdout := &cancelOn{word: "HOOK PENDING", cancel: func() {
		watched, err := notice.ClaudeWatched(state)
		assert.NoError(t, err)
		assert.True(t, watched, "the lock is held while the watcher runs")
		cancel()
	}}
	code = hookTool(w).RunContext(ctx, args, strings.NewReader(""), stdout, &errs)
	assert.Contains(t, stdout.b.String(), "state=watching")
	assert.Contains(t, stdout.b.String(), pending, "a notice pushed before the watcher was armed is told at arming")
	_ = code // ended by its context: the closing line names it
	watched, err := notice.ClaudeWatched(state)
	require.NoError(t, err)
	assert.False(t, watched, "the lock goes when the watcher ends")
}
