package friend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// codexThread writes a saved Codex thread's rollout under home: in sessions/
// (live), or in archived_sessions/ (archived, what the app's archive does),
// its header naming cwd.
func codexThread(t *testing.T, home, id, cwd string, archived bool) {
	t.Helper()
	dir := filepath.Join(home, "sessions", "2026", "10", "06")
	if archived {
		dir = filepath.Join(home, "archived_sessions")
	}
	require.NoError(t, os.MkdirAll(dir, 0o755))
	header := `{"type":"session_meta","payload":{"id":"` + id + `","cwd":"` + cwd + `","timestamp":"2026-10-06T10:00:00Z"}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rollout-2026-10-06T10-00-00-"+id+".jsonl"), []byte(header), 0o644))
}

// A named session that is gone is target-invalid: never a silent defer loop
// (the finding of 2026-10-06, a friend's own diagnosis: her service named
// an archived Codex thread, every delivery was deferred and retried for
// hours, the daemon stayed up, her proof and beat went stale, the sprint
// read her down, and nothing named the cause). The first try goes in and is
// deferred, as it was; before the retry the adapter reads the named thread's
// lifecycle, and archived, it is one blocker to the coordinator and one NOTE
// to the friend naming the target, the state found and the rebind command,
// the status says session=target-invalid, nothing more is handed in, and the
// message stays pending for the rebound session, never acked and never given
// up. (run reads it at the start, so a restarted daemon tries nothing:
// TestRunStartsTargetInvalidOnAGoneSession.)
func TestAGoneSessionTargetIsInvalidAndNeverRetried(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		home, dir := t.TempDir(), t.TempDir()
		const thread = "01a10e84-0000-4000-8000-000000000001"
		codexThread(t, home, thread, dir, true)
		var mu sync.Mutex
		calls := 0
		run := func(context.Context, string, string, []string, string) (string, int, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			return "error: thread " + thread + " not found\n", 1, nil // what an archived thread answers on both routes
		}
		r.d.Deliver, r.passive = &Codex{Dir: dir, Session: thread, Run: run, Home: home, Held: func(string) bool { return true }}, true
		r.d.Harness, r.d.Coordinator = "codex", "ada"
		r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
		r.send(t, "ada", "hello", "x")
		r.run(t, 10*int(RecheckEvery/BeatEvery)) // ten rechecks' worth of steps

		mu.Lock()
		assert.Equal(t, 2, calls, "the first try on both routes, and no retry")
		mu.Unlock()
		s := r.last()
		assert.Equal(t, "target-invalid", s.Session)
		assert.Equal(t, thread, s.SessionID)
		assert.Contains(t, s.SessionReason, "archived")

		deferred := 0
		for _, line := range r.records {
			if strings.Contains(line, "deferred=") {
				deferred++
			}
			assert.NotContains(t, line, "given_up")
		}
		assert.Equal(t, 1, deferred, "deferred once, never again: %q", r.records)
		told := r.adaGot(t)
		blockers := 0
		for _, m := range told {
			if strings.Contains(m, "target-invalid") {
				blockers++
				assert.Contains(t, m, thread)
				assert.Contains(t, m, "archived")
				assert.Contains(t, m, "nova-friend rebind --as bob --session <id>")
			}
		}
		assert.Equal(t, 1, blockers, "one judgment to the coordinator: %q", told)

		pending, fresh, err := r.bus.Peek(context.Background(), "bob")
		require.NoError(t, err)
		notes, hello := 0, 0
		for _, e := range append(pending, fresh...) {
			m := e.Message()
			switch {
			case m.Subject == "hello":
				hello++
			case strings.HasPrefix(m.Subject, "NOTE"):
				notes++
				assert.Contains(t, m.Body, thread)
				assert.Contains(t, m.Body, "archived")
				assert.Contains(t, m.Body, "nova-friend rebind --as bob --session <id>")
			}
		}
		assert.Equal(t, 1, hello, "the message stays on her stream for the rebound session")
		assert.Equal(t, 1, notes, "one NOTE to the friend")
		assert.Equal(t, 0, s.Delivered)
	})
}

// A thread live at the first try and archived before the retry: the first
// delivery is deferred as today, the retry reads the lifecycle first and
// finds it archived, so nothing more is handed in (tla/Delivery.tla,
// RetryChecksFirst).
func TestATargetArchivedWhileDeferredIsInvalidAtTheRetry(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		home, dir := t.TempDir(), t.TempDir()
		const thread = "01a10e84-0000-4000-8000-000000000002"
		codexThread(t, home, thread, dir, false)
		var mu sync.Mutex
		calls := 0
		run := func(context.Context, string, string, []string, string) (string, int, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls == 2 { // both routes refused: the friend archives the thread meanwhile
				live := filepath.Join(home, "sessions", "2026", "10", "06", "rollout-2026-10-06T10-00-00-"+thread+".jsonl")
				require.NoError(t, os.MkdirAll(filepath.Join(home, "archived_sessions"), 0o755))
				require.NoError(t, os.Rename(live, filepath.Join(home, "archived_sessions", filepath.Base(live))))
			}
			return "busy\n", 1, nil
		}
		r.d.Deliver, r.passive = &Codex{Dir: dir, Session: thread, Run: run, Home: home, Held: func(string) bool { return true }}, true
		r.d.Harness, r.d.Coordinator = "codex", "ada"
		r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
		r.send(t, "ada", "hello", "x")
		r.run(t, 10*int(RecheckEvery/BeatEvery))
		mu.Lock()
		assert.Equal(t, 2, calls, "one deferred try on both routes, then no retry")
		mu.Unlock()
		deferred := 0
		for _, line := range r.records {
			if strings.Contains(line, "deferred=") {
				deferred++
			}
		}
		assert.Equal(t, 1, deferred)
		assert.Equal(t, SessionTargetInvalid, r.last().Session)
		assert.Contains(t, r.last().SessionReason, TargetArchived)
	})
}

// Every adapter's terminal session states, one case each, as
// docs/SPEC-FRIEND.md lists them (TerminalStates); a live target answers nil.
func TestEveryAdapterReadsItsTerminalSessionStates(t *testing.T) {
	t.Parallel()
	listing := func(json string) Exec {
		return func(context.Context, string, string, []string, string) (string, int, error) { return json, 0, nil }
	}
	type tc struct {
		harness, state string
		adapter        func(t *testing.T) TargetChecker
	}
	cases := []tc{
		{"codex", "", func(t *testing.T) TargetChecker {
			home, dir := t.TempDir(), t.TempDir()
			codexThread(t, home, "th-live", dir, false)
			return &Codex{Dir: dir, Session: "th-live", Home: home}
		}},
		{"codex", TargetArchived, func(t *testing.T) TargetChecker {
			home, dir := t.TempDir(), t.TempDir()
			codexThread(t, home, "th-a", dir, true)
			return &Codex{Dir: dir, Session: "th-a", Home: home}
		}},
		{"codex", TargetDeleted, func(t *testing.T) TargetChecker {
			home, dir := t.TempDir(), t.TempDir()
			codexThread(t, home, "th-other", dir, false)
			return &Codex{Dir: dir, Session: "th-d", Home: home}
		}},
		{"codex", TargetMoved, func(t *testing.T) TargetChecker {
			home, dir := t.TempDir(), t.TempDir()
			codexThread(t, home, "th-m", t.TempDir(), false)
			return &Codex{Dir: dir, Session: "th-m", Home: home}
		}},
		{"opencode", "", func(t *testing.T) TargetChecker {
			return &OpenCode{Dir: "/w/bob", Session: "ses_1", Run: listing(`[{"id":"ses_1","directory":"/w/bob","updated":1}]`)}
		}},
		{"opencode", TargetDeleted, func(t *testing.T) TargetChecker {
			return &OpenCode{Dir: "/w/bob", Session: "ses_1", Run: listing(`[{"id":"ses_2","directory":"/w/bob","updated":1}]`)}
		}},
		{"opencode", TargetMoved, func(t *testing.T) TargetChecker {
			return &OpenCode{Dir: "/w/bob", Session: "ses_1", Run: listing(`[{"id":"ses_1","directory":"/w/amy","updated":1}]`)}
		}},
		{"dsh", "", func(t *testing.T) TargetChecker {
			root, dir := t.TempDir(), t.TempDir()
			real, err := filepath.EvalSymlinks(dir)
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(filepath.Join(root, DSHSessionKey(real), "session-1"), 0o755))
			return &DSH{Dir: dir, Session: "session-1", Sessions: root}
		}},
		{"dsh", TargetDeleted, func(t *testing.T) TargetChecker {
			return &DSH{Dir: t.TempDir(), Session: "session-1", Sessions: t.TempDir()}
		}},
		{"dsh", TargetMoved, func(t *testing.T) TargetChecker {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, "--w-amy--", "session-1"), 0o755))
			return &DSH{Dir: t.TempDir(), Session: "session-1", Sessions: root}
		}},
		{"grok", "", func(t *testing.T) TargetChecker {
			wake := filepath.Join(t.TempDir(), "bob.wake")
			require.NoError(t, os.WriteFile(wake, nil, 0o644))
			return &Grok{Wake: wake}
		}},
		{"grok", TargetDeleted, func(t *testing.T) TargetChecker {
			return &Grok{Wake: filepath.Join(t.TempDir(), "bob.wake")}
		}},
		{"tmux", "", func(t *testing.T) TargetChecker {
			return &Tmux{Session: "%3", Run: listing("%1\n%3\n")}
		}},
		{"tmux", TargetDeleted, func(t *testing.T) TargetChecker {
			return &Tmux{Session: "%3", Run: listing("%1\n%2\n")}
		}},
		{"tmux", "", func(t *testing.T) TargetChecker { // a session name host makes again is never terminal
			return &Tmux{Session: "friend-bob", Run: listing("")}
		}},
	}
	covered := map[string]map[string]bool{}
	for _, c := range cases {
		err := c.adapter(t).CheckTarget(context.Background())
		if c.state == "" {
			assert.NoError(t, err, "%s live", c.harness)
			continue
		}
		var gone TargetInvalid
		require.ErrorAs(t, err, &gone, "%s %s", c.harness, c.state)
		assert.Equal(t, c.state, gone.State)
		assert.Equal(t, c.harness, gone.Harness)
		if covered[c.harness] == nil {
			covered[c.harness] = map[string]bool{}
		}
		covered[c.harness][c.state] = true
	}
	for _, h := range Harnesses {
		states, listed := TerminalStates[h]
		_, none := NoTerminalStates[h]
		_, surveyed := Refusals[h]
		assert.True(t, listed != none || surveyed, "%s: in exactly one of TerminalStates and NoTerminalStates", h)
		for _, s := range states {
			assert.True(t, covered[h][s.State], "%s %s has a case", h, s.State)
		}
		d, err := NewDeliverer(h, "", "", nil, nil)
		require.NoError(t, err)
		_, checks := d.(TargetChecker)
		assert.Equal(t, listed, checks, "%s: lists terminal states exactly when its adapter reads them", h)
	}
}

// The bound target: a rebind retires the session it replaces, and a service
// started or installed with a retired id is refused, so a reinstall cannot
// resurrect it; binding a retired id again (a live one, by rebind) takes it
// off the list.
func TestABoundTargetRefusesTheSessionItReplaced(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	_, found, err := ReadTarget(state)
	require.NoError(t, err)
	assert.False(t, found)
	tg := Target{}.Bind("bob", "codex", "old", t0)
	assert.Empty(t, tg.Refuses("old"))
	tg = tg.Bind("bob", "codex", "new", t0.Add(time.Hour))
	require.NoError(t, WriteTarget(state, tg))
	back, found, err := ReadTarget(state)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, []string{"old"}, back.Retired)
	assert.Contains(t, back.Refuses("old"), "nova-friend rebind --as bob --session <id>")
	assert.Empty(t, back.Refuses("new"))
	assert.Empty(t, back.Refuses(""))
	again := back.Bind("bob", "codex", "old", t0.Add(2*time.Hour))
	assert.Equal(t, []string{"new"}, again.Retired)
	assert.Empty(t, again.Refuses("old"))
}

// liveDSHSession makes a DSH sessions root under the test's temp tree in
// which session is a live session of dir, and answers the root.
func liveDSHSession(t *testing.T, dir, session string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, DSHSessionKey(real), session), 0o700))
	return root
}
