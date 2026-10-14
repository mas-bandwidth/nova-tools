package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
)

const hookedInstalled = "v9.9.9-test"

// friendsHookedEnv is a PATH with a nova-hook the check can see, and an exec
// that answers version and friends from the arguments. Nothing is started.
// clock is the time argument on the Env. The check does not call time.Now or
// sleep: up and down are the faked record's own state.
func friendsHookedEnv(t *testing.T, clock time.Time, installed, friendsOut string, friendsErr error) Env {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bin, "nova-hook"), []byte("#!/bin/sh\n"), 0o755))
	return fakeEnv{
		env:   map[string]string{"PATH": "bin"},
		root:  root,
		clock: clock,
		exec: func(name string, args ...string) (string, error) {
			require.Equal(t, "nova-hook", filepath.Base(name))
			switch strings.Join(args, " ") {
			case "version":
				if installed == "" {
					return "usage: nope\n", nil
				}
				return buildinfo.Line("nova-hook", installed), nil
			case "friends --json":
				return friendsOut, friendsErr
			default:
				return "", errors.New("unexpected " + strings.Join(args, " "))
			}
		},
	}
}

func hookedFriendsJSON(t *testing.T, serve string, rows ...hookedFriend) string {
	t.Helper()
	items := make([]any, 0, len(rows))
	for _, r := range rows {
		fields := map[string]any{"name": r.name, "role": r.role, "state": r.state, "reason": r.reason}
		if r.version != "" {
			fields["version"] = r.version
		}
		items = append(items, map[string]any{"kind": "name", "fields": fields})
	}
	b, err := json.Marshal(map[string]any{
		"facts": map[string]any{"serve_version": serve},
		"items": items,
	})
	require.NoError(t, err)
	return string(b)
}

func runFriendsHooked(t *testing.T, env Env) Result {
	t.Helper()
	got, _, err := Default.Run(context.Background(), env, Options{Only: []string{"friends-hooked"}})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "friends-hooked", got[0].Check)
	assert.False(t, Default.checks["friends-hooked"].Fleet)
	return got[0]
}

func TestDoctorFriendsHooked(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

	t.Run("a_friend_who_is_not_up_fails", func(t *testing.T) {
		t.Parallel()
		raw := hookedFriendsJSON(t, hookedInstalled,
			hookedFriend{name: "ada", role: "coordinator", state: "up", version: hookedInstalled},
			hookedFriend{name: "bob", role: "friend", state: "down", version: hookedInstalled, reason: "missed 2 challenges"})
		r := runFriendsHooked(t, friendsHookedEnv(t, now, hookedInstalled, raw, nil))
		assert.Equal(t, Fail, r.Status)
		assert.Equal(t, "bob is not up (missed 2 challenges)", r.Evidence)
		assert.Equal(t, "nova-hook hook --as bob --role friend", r.Fix)
	})

	t.Run("a_friend_up_on_the_wrong_version_fails", func(t *testing.T) {
		t.Parallel()
		raw := hookedFriendsJSON(t, hookedInstalled,
			hookedFriend{name: "bob", role: "comms", state: "up", version: "v9.9.8-test"})
		r := runFriendsHooked(t, friendsHookedEnv(t, now, hookedInstalled, raw, nil))
		assert.Equal(t, Fail, r.Status)
		assert.Equal(t, "bob is up on v9.9.8-test, installed nova-hook is v9.9.9-test", r.Evidence)
		assert.Equal(t, "nova-update apply --file <manifest> nova-hook --version v9.9.9-test", r.Fix)
	})

	t.Run("a_friend_up_with_no_version_fails", func(t *testing.T) {
		t.Parallel()
		raw := hookedFriendsJSON(t, hookedInstalled,
			hookedFriend{name: "bob", role: "friend", state: "up"})
		r := runFriendsHooked(t, friendsHookedEnv(t, now, hookedInstalled, raw, nil))
		assert.Equal(t, Fail, r.Status)
		assert.Equal(t, "bob is up and reports no nova-hook version, installed nova-hook is v9.9.9-test", r.Evidence)
		assert.Contains(t, r.Fix, "nova-update apply")
	})

	t.Run("serve_version_differs_fails", func(t *testing.T) {
		t.Parallel()
		raw := hookedFriendsJSON(t, "v9.9.8-test",
			hookedFriend{name: "bob", role: "friend", state: "up", version: hookedInstalled})
		r := runFriendsHooked(t, friendsHookedEnv(t, now, hookedInstalled, raw, nil))
		assert.Equal(t, Fail, r.Status)
		assert.Equal(t, "nova-hook serve reports v9.9.8-test, installed nova-hook is v9.9.9-test", r.Evidence)
		assert.Equal(t, "nova-update apply --file <manifest> nova-hook --version v9.9.9-test", r.Fix)
	})

	t.Run("serve_with_no_version_fails", func(t *testing.T) {
		t.Parallel()
		raw := hookedFriendsJSON(t, "",
			hookedFriend{name: "bob", role: "friend", state: "up", version: hookedInstalled})
		r := runFriendsHooked(t, friendsHookedEnv(t, now, hookedInstalled, raw, nil))
		assert.Equal(t, Fail, r.Status)
		assert.Equal(t, "nova-hook serve reports no version, installed nova-hook is v9.9.9-test", r.Evidence)
	})

	t.Run("a_friend_up_on_the_installed_version_passes", func(t *testing.T) {
		t.Parallel()
		raw := hookedFriendsJSON(t, hookedInstalled,
			hookedFriend{name: "bob", role: "friend", state: "up", version: hookedInstalled},
			hookedFriend{name: "cara", role: "comms", state: "up", version: hookedInstalled})
		r := runFriendsHooked(t, friendsHookedEnv(t, now, hookedInstalled, raw, nil))
		assert.Equal(t, OK, r.Status)
		assert.Equal(t, "2 friends on v9.9.9-test; nova-hook serve is v9.9.9-test", r.Evidence)
		assert.Empty(t, r.Fix)
	})

	t.Run("a_serve_whose_version_matches_passes", func(t *testing.T) {
		t.Parallel()
		// A down coordinator is not a friend. The serve match is the pass.
		raw := hookedFriendsJSON(t, hookedInstalled,
			hookedFriend{name: "ada", role: "coordinator", state: "down", reason: "hook server stale"})
		r := runFriendsHooked(t, friendsHookedEnv(t, now, hookedInstalled, raw, nil))
		assert.Equal(t, OK, r.Status)
		assert.Equal(t, "no friends in the hook record; nova-hook serve is v9.9.9-test", r.Evidence)
		assert.Empty(t, r.Fix)
	})

	t.Run("nova_hook_missing_fails", func(t *testing.T) {
		t.Parallel()
		r := runFriendsHooked(t, fakeEnv{clock: now})
		assert.Equal(t, Fail, r.Status)
		assert.Equal(t, "nova-hook is not on PATH", r.Evidence)
		assert.NotEmpty(t, r.Fix)
	})
}
