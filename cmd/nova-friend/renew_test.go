package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeOpenCode is the harness through the Exec seam: its session listing
// holds the old session until a run with no --session opens ses_new.
type fakeOpenCode struct {
	mu    sync.Mutex
	dir   string
	calls [][]string
	open  bool
}

func (f *fakeOpenCode) exec(_ context.Context, _, name string, args []string, _ string) (string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string{name}, args...))
	if len(args) > 1 && args[0] == "session" && args[1] == "list" {
		rows := []map[string]any{{"id": "ses_old", "directory": f.dir, "updated": 1}}
		if f.open {
			rows = append(rows, map[string]any{"id": "ses_new", "directory": f.dir, "updated": 2})
		}
		raw, err := json.Marshal(rows)
		return string(raw), 0, err
	}
	if len(args) > 0 && args[0] == "run" && !slices.Contains(args, "--session") {
		f.open = true
	}
	return "ready\n", 0, nil
}

// seed is the text of the run that opened the new session.
func (f *fakeOpenCode) seed() string {
	for _, c := range f.calls {
		if len(c) > 1 && c[1] == "run" {
			return c[len(c)-1]
		}
	}
	return ""
}

// renewRig is bob's daemon state: his directory with his own files, the
// status file (broken in ses_old when broken), and, when installed, his
// agent pinned to ses_old; the harness is the fake.
type renewRig struct {
	*rig
	dir, state string
	agent      friend.Agent
	oc         *fakeOpenCode
}

func newRenewRig(t *testing.T, harness string, installed, broken bool) *renewRig {
	t.Helper()
	r := &renewRig{rig: newRig(t, "ada", "bob"), dir: t.TempDir()}
	r.state = friend.DefaultStateDir(r.home, "bob")
	r.oc = &fakeOpenCode{dir: r.dir}
	require.NoError(t, os.WriteFile(filepath.Join(r.dir, "AGENTS.md"), []byte("bob's own words, never copied\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(r.dir, "memory"), 0o755))
	s := friend.Status{Friend: "bob", Harness: harness, Dir: r.dir, Pinned: "ses_old", Session: friend.SessionOK}
	if broken {
		s.Session, s.SessionID, s.SessionReason, s.BrokenAt = friend.SessionBroken, "ses_old", "invalid_request_error: The request could not be processed", start
	}
	require.NoError(t, friend.WriteStatus(r.state, s))
	r.agent = friend.Agent{Friend: "bob", Harness: harness, Dir: r.dir, Session: "ses_old", Binary: "/opt/nova/bin/nova-friend", Redis: "store.test:6379", Server: "server.test:6390", Home: r.home}
	if installed {
		require.NoError(t, os.MkdirAll(filepath.Dir(r.agent.PlistPath()), 0o755))
		require.NoError(t, os.WriteFile(r.agent.PlistPath(), []byte(r.agent.Plist()), 0o644))
	}
	return r
}

func (r *renewRig) cli() testkit.Main {
	w := r.world()
	w.exec = r.oc.exec
	return cliOf(w)
}

func TestRenewStartsAFreshSessionAndRepinsTheDaemon(t *testing.T) {
	t.Parallel()
	for _, asJSON := range []bool{false, true} {
		t.Run(map[bool]string{false: "lines", true: "json"}[asJSON], func(t *testing.T) {
			t.Parallel()
			r := newRenewRig(t, "opencode", true, true)
			args := []string{"renew", "--as", "ada", "bob"}
			if asJSON {
				args = append(args, "--json")
			}
			ran := r.cli().Do(t, args...).Exit(0)
			if asJSON {
				ran.Out(`"friend":"bob"`, `"old":"ses_old"`, `"new":"ses_new"`, `"kind":"new"`, `"kind":"seeded"`, `"kind":"pinned"`)
			} else {
				ran.Out("RENEW OK friend=bob old=ses_old new=ses_new",
					"RENEW NEW friend=bob harness=opencode old=ses_old new=ses_new",
					"RENEW SEEDED friend=bob bytes=",
					"RENEW PINNED friend=bob session=ses_new")
			}

			plist, err := os.ReadFile(r.agent.PlistPath())
			require.NoError(t, err)
			args, err = friend.AgentArgs(string(plist))
			require.NoError(t, err)
			assert.Equal(t, "ses_new", friend.ArgValue(args, "--session"), "the agent is pinned to the new session")
			assert.Equal(t, strings.Replace(r.agent.Plist(), "ses_old", "ses_new", 1), string(plist), "and the rest of the plist is kept")

			s, found, err := friend.ReadStatus(r.state)
			require.NoError(t, err)
			require.True(t, found)
			assert.Equal(t, "ses_new", s.Pinned)
			assert.Equal(t, friend.SessionOK, s.Session, "the broken mark is cleared")
			assert.Empty(t, s.SessionID)
			assert.Empty(t, s.SessionReason)

			assert.Equal(t, []string{"bootout gui/501/com.nova.friend-bob", "bootstrap gui/501 " + r.agent.PlistPath()}, r.launchctl, "the daemon is restarted")
		})
	}
}

func TestRenewNeverTouchesTheOldSession(t *testing.T) {
	t.Parallel()
	r := newRenewRig(t, "opencode", false, false)
	r.cli().Do(t, "renew", "--as", "ada", "bob", "--reason", "the context is full").Exit(0).
		Out("RENEW OK friend=bob old=ses_old new=ses_new", "RENEW NOTE no agent is installed for bob: restart its daemon with --session ses_new")
	require.NotEmpty(t, r.oc.calls)
	for _, c := range r.oc.calls {
		assert.False(t, slices.Contains(c, "--session"), "no command is sent into a session: %q", c)
		assert.NotContains(t, c, "delete", "no session is deleted: %q", c)
		ran := []string{c[1], c[2]}
		assert.Contains(t, [][]string{{"session", "list"}, {"run", "--dir"}}, ran, "only the listing and a new session run: %q", c)
	}
	assert.Empty(t, r.launchctl, "no agent, nothing restarted")
	s, _, err := friend.ReadStatus(r.state)
	require.NoError(t, err)
	assert.Equal(t, "ses_new", s.Pinned, "the state file is re-pinned")
}

func TestRenewRefusesAHarnessWithNoNewSessionRoute(t *testing.T) {
	t.Parallel()
	for _, h := range []string{"codex", "dsh", "grok", "antigravity", "gemini", "claude", "cursor"} {
		t.Run(h, func(t *testing.T) {
			t.Parallel()
			r := newRenewRig(t, h, true, true)
			before, err := os.ReadFile(r.agent.PlistPath())
			require.NoError(t, err)
			r.cli().Do(t, "renew", "--as", "ada", "bob").Exit(1).
				Refused("harness " + h + " has no new-session route this tool drives; nothing was changed").
				Err("--session <its id>")
			assert.Empty(t, r.oc.calls, "no harness command runs")
			assert.Empty(t, r.launchctl, "the daemon is not restarted")
			after, err := os.ReadFile(r.agent.PlistPath())
			require.NoError(t, err)
			assert.Equal(t, before, after, "the agent is not touched")
			s, _, err := friend.ReadStatus(r.state)
			require.NoError(t, err)
			assert.Equal(t, friend.SessionBroken, s.Session, "the broken mark stays")
		})
	}
	t.Run("no daemon state", func(t *testing.T) {
		t.Parallel()
		newRig(t).cli().Do(t, "renew", "--as", "ada", "zed").Exit(1).Refused("no daemon state for zed")
	})
	t.Run("no reason and not broken", func(t *testing.T) {
		t.Parallel()
		r := newRenewRig(t, "opencode", false, false)
		r.cli().Do(t, "renew", "--as", "ada", "bob").Exit(2).Refused("--reason is required")
		assert.Empty(t, r.oc.calls)
	})
}

func TestRenewSeedNamesTheIdentityFilesTheOldSessionAndTheReason(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, reason string
		args         []string
	}{
		{"the broken mark's reason", "invalid_request_error: The request could not be processed", nil},
		{"--reason over it", "the coordinator asked", []string{"--reason", "the coordinator asked"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRenewRig(t, "opencode", true, true)
			r.cli().Do(t, append([]string{"renew", "--as", "ada", "bob", "--redis", "store.test:6379"}, c.args...)...).Exit(0)
			seed := r.oc.seed()
			for _, want := range []string{
				"You are bob",
				"ada renewed your session",
				filepath.Join(r.dir, "AGENTS.md"),
				filepath.Join(r.dir, "memory") + "/",
				"The old session is ses_old",
				"Why it was renewed: " + c.reason,
				"/opt/nova/bin/nova-friend pong --as bob --nonce <nonce> --to ada --state-dir " + r.state + " --redis store.test:6379",
			} {
				assert.Contains(t, seed, want)
			}
			assert.NotContains(t, seed, "bob's own words", "the identity files are named, never copied")
		})
	}
}

func TestRenewDryRunChangesNothing(t *testing.T) {
	t.Parallel()
	r := newRenewRig(t, "opencode", true, true)
	plist, err := os.ReadFile(r.agent.PlistPath())
	require.NoError(t, err)
	status, err := os.ReadFile(filepath.Join(r.state, friend.StatusFile))
	require.NoError(t, err)
	r.cli().Do(t, "renew", "bob", "--as", "ada", "--dry-run").Exit(0).
		Out("RENEW OK friend=bob harness=opencode dir="+r.dir+" old=ses_old bytes=", "dry_run=true",
			"RENEW PLAN command=\"opencode run --dir "+r.dir+" <the wake brief>\"",
			"RENEW PLAN command=\"launchctl bootout gui/501/com.nova.friend-bob\"",
			"RENEW PLAN command=\"launchctl bootstrap gui/501 "+r.agent.PlistPath()+"\"")
	assert.Empty(t, r.oc.calls, "no harness command runs")
	assert.Empty(t, r.launchctl, "launchctl is not run")
	after, err := os.ReadFile(r.agent.PlistPath())
	require.NoError(t, err)
	assert.Equal(t, plist, after)
	afterStatus, err := os.ReadFile(filepath.Join(r.state, friend.StatusFile))
	require.NoError(t, err)
	assert.Equal(t, status, afterStatus)
}

func TestRenewRefusesUnreadableAgentBeforeStartingASession(t *testing.T) {
	t.Parallel()
	r := newRenewRig(t, "opencode", false, true)
	require.NoError(t, os.MkdirAll(r.agent.PlistPath(), 0o755))
	before, err := os.ReadFile(filepath.Join(r.state, friend.StatusFile))
	require.NoError(t, err)
	r.cli().Do(t, "renew", "--as", "ada", "bob").Exit(2).
		Refused("the agent plist cannot be read").Err(r.agent.PlistPath())
	assert.Empty(t, r.oc.calls)
	assert.Empty(t, r.launchctl)
	after, err := os.ReadFile(filepath.Join(r.state, friend.StatusFile))
	require.NoError(t, err)
	assert.Equal(t, before, after)
}
