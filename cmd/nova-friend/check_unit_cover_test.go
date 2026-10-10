package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/require"
)

// TestNovaFriendCheckCoverMissingShownFile tests --shown naming a missing file
func TestNovaFriendCheckCoverMissingShownFile(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	cli.Do(t, "check", "--as", "ada", "--shown", "/nonexistent.json", "bob").Exit(2).Err("cannot be read")
}

// TestNovaFriendCheckCoverInvalidShownJSON tests --shown naming a file of invalid JSON
func TestNovaFriendCheckCoverInvalidShownJSON(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	shownFile := filepath.Join(t.TempDir(), "shown.json")
	require.NoError(t, os.WriteFile(shownFile, []byte(`{invalid}`), 0o644))
	cli.Do(t, "check", "--as", "ada", "--shown", shownFile, "bob").Exit(2).Err("is invalid JSON")
}

// TestNovaFriendCheckCoverShownFromStdin tests --shown - with JSON on stdin
func TestNovaFriendCheckCoverShownFromStdin(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	state := friend.DefaultStateDir(r.home, "bob")
	require.NoError(t, friend.WriteStatus(state, friend.Status{
		Friend:     "bob",
		Harness:    "opencode",
		At:         start,
		Connection: friend.Connected,
	}))
	// stdin with JSON
	input := `{"bob":{"state":"up","working":1}}`
	cli.DoIn(t, input, "check", "--as", "ada", "--shown", "-", "bob").Exit(1).
		Out("shown=up/1")
}

// TestNovaFriendCheckCoverStoreFail tests r.store.Fail set
func TestNovaFriendCheckCoverStoreFail(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.store.Fail = bus.NewErr("store is down")
	cli := r.cli()
	state := friend.DefaultStateDir(r.home, "bob")
	require.NoError(t, friend.WriteStatus(state, friend.Status{
		Friend:     "bob",
		Harness:    "opencode",
		At:         start,
		Connection: friend.Connected,
	}))
	cli.Do(t, "check", "--as", "ada", "bob").Exit(2).Err("store is down")
}

// TestNovaFriendCheckCoverNoPosFriendFromStore tests no positional friend with r.store.Friends set
func TestNovaFriendCheckCoverNoPosFriendFromStore(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.store.Friends = []string{"bob"}
	cli := r.cli()
	state := friend.DefaultStateDir(r.home, "bob")
	r.launchctlOut = "12345 0 com.nova.friend-bob\n"
	require.NoError(t, friend.WriteStatus(state, friend.Status{
		Friend:     "bob",
		Harness:    "opencode",
		At:         start,
		Connection: friend.Connected,
	}))
	require.NoError(t, friend.WritePresence(state, friend.PresenceStatus{
		Friend:    "bob",
		Presence:  friend.PresenceUp,
		At:        start,
		LastHeard: start,
	}))
	require.NoError(t, friend.WritePong(state, friend.Pong{
		Nonce: "n0",
		At:    start,
	}))
	require.NoError(t, friend.Record(state, "2026-10-04T02:50:00Z subject=work messages=1 took=3s exit=0 acked=true"))
	cli.Do(t, "check", "--as", "ada").Exit(0).Out("friends=1")
}

// TestNovaFriendCheckCoverNoPosFriendFromFS tests no positional friend, no store, and <home>/.nova-friend holding directories
func TestNovaFriendCheckCoverNoPosFriendFromFS(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.env = map[string]string{} // no Redis
	// create <home>/.nova-friend with zed and amy directories
	friendsDir := filepath.Join(r.home, ".nova-friend")
	require.NoError(t, os.MkdirAll(friendsDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(friendsDir, "amy"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(friendsDir, "zed"), 0o755))
	// add a plain file too (should be ignored)
	require.NoError(t, os.WriteFile(filepath.Join(friendsDir, "plain.txt"), []byte("ignored"), 0o644))
	cli := r.cli()
	// setup state for amy and zed
	for _, f := range []string{"amy", "zed"} {
		state := friend.DefaultStateDir(r.home, f)
		require.NoError(t, friend.WriteStatus(state, friend.Status{
			Friend:     f,
			Harness:    "opencode",
			At:         start,
			Connection: friend.Connected,
		}))
		require.NoError(t, friend.WritePresence(state, friend.PresenceStatus{
			Friend:    f,
			Presence:  friend.PresenceUp,
			At:        start,
			LastHeard: start,
		}))
		require.NoError(t, friend.WritePong(state, friend.Pong{
			Nonce: "n0",
			At:    start,
		}))
		require.NoError(t, friend.Record(state, "2026-10-04T02:50:00Z subject=work messages=1 exit=0"))
	}
	cli.Do(t, "check", "--as", "ada").Exit(0).Out("friends=2").Out("friend=amy").Out("friend=zed")
}

// TestNovaFriendCheckCoverNoPosFriendFromFSEmpty tests no positional friend, no store, no <home>/.nova-friend
func TestNovaFriendCheckCoverNoPosFriendFromFSEmpty(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.env = map[string]string{} // no Redis
	// ensure <home>/.nova-friend does not exist
	// cli
(t, "check", "--as", "ada").Exit(0).Out("friends=0")
}

// TestNovaFriendCheckCoverStateDirSubdir tests --state-dir D where D/bob holds a status
func TestNovaFriendCheckCoverStateDirSubdir(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	stateDir := t.TempDir()
	// create D/bob with status
	bobState := filepath.Join(stateDir, "bob")
	require.NoError(t, os.MkdirAll(bobState, 0o755))
	require.NoError(t, friend.WriteStatus(bobState, friend.Status{
		Friend:     "bob",
		Harness:    "opencode",
		At:         start,
		Connection: friend.Connected,
	}))
	require.NoError(t, friend.WritePresence(bobState, friend.PresenceStatus{
		Friend:    "bob",
		Presence:  friend.PresenceUp,
		At:        start,
		LastHeard: start,
	}))
	require.NoError(t, friend.WritePong(bobState, friend.Pong{
		Nonce: "n0",
		At:    start,
	}))
	require.NoError(t, friend.Record(bobState, "2026-10-04T02:50:00Z subject=work messages=1 exit=0"))
	cli := r.cli()
	cli.Do(t, "check", "--as", "ada", "--state-dir", stateDir, "bob").Exit(0).Out("friends=1")
}

// TestNovaFriendCheckCoverStateDirOnly tests --state-dir D where only D itself holds status (no per-friend subdir)
func TestNovaFriendCheckCoverStateDirOnly(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	stateDir := t.TempDir()
	// put status directly in D (not D/bob)
	require.NoError(t, friend.WriteStatus(stateDir, friend.Status{
		Friend:     "bob",
		Harness:    "opencode",
		At:         start,
		Connection: friend.Connected,
	}))
	cli := r.cli()
	cli.Do(t, "check", "--as", "ada", "--state-dir", stateDir, "bob").Exit(1).Out("friends=1")
}

// TestNovaFriendCheckCoverPlistStateDir tests plist with --state-dir
func TestNovaFriendCheckCoverPlistStateDir(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	stateDir := t.TempDir()
	// write status under S
	require.NoError(t, friend.WriteStatus(stateDir, friend.Status{
		Friend:     "bob",
		Harness:    "opencode",
		At:         start,
		Connection: friend.Connected,
	}))
	require.NoError(t, friend.WritePresence(stateDir, friend.PresenceStatus{
		Friend:    "bob",
		Presence:  friend.PresenceUp,
		At:        start,
		LastHeard: start,
	}))
	require.NoError(t, friend.WritePong(stateDir, friend.Pong{
		Nonce: "n0",
		At:    start,
	}))
	require.NoError(t, friend.Record(stateDir, "2026-10-04T02:50:00Z subject=work messages=1 exit=0"))
	// write plist with --state-dir
	plistPath := filepath.Join(r.home, "Library", "LaunchAgents", "com.nova.friend-bob.plist")
	agent := friend.Agent{
		Friend: "bob",
		Home:   r.home,
		StateDir: stateDir,
	}
	plist := agent.Plist()
	require.NoError(t, os.MkdirAll(filepath.Dir(plistPath), 0o755))
	require.NoError(t, os.WriteFile(plistPath, []byte(plist), 0o644))
	r.launchctlOut = "12345 0 com.nova.friend-bob\n"
	cli := r.cli()
	cli.Do(t, "check", "--as", "ada", "bob").Exit(0).Out("friends=1")
}

// TestNovaFriendCheckCoverDir tests --dir W where W/inbox holds two files
func TestNovaFriendCheckCoverDir(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	workDir := t.TempDir()
	inboxDir := filepath.Join(workDir, "inbox")
	require.NoError(t, os.MkdirAll(inboxDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(inboxDir, "f1"), []byte("1"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(inboxDir, "f2"), []byte("2"), 0o644))
	cli := r.cli()
	cli.Do(t, "check", "--as", "ada", "--dir", workDir, "bob").Exit(1).Out("inbox=2")
}

// TestNovaFriendCoverAfter tests argAfter with flag last and no value, absent, and present
func TestNovaFriendCoverAfter(t *testing.T) {
	t.Parallel()
	// flag last and no value
	if got := argAfter([]string{"--flag"}, "--flag"); got != "" {
		t.Errorf("argAfter(%v, --flag) = %q, want empty", []string{"--flag"}, got)
	}
	// absent
	if got := argAfter([]string{"--other"}, "--flag"); got != "" {
		t.Errorf("argAfter(%v, --flag) = %q, want empty", []string{"--other"}, got)
	}
	// present
	if got := argAfter([]string{"--flag", "value"}, "--flag"); got != "value" {
		t.Errorf("argAfter(%v, --flag) = %q, want value", []string{"--flag", "value"}, got)
	}
}
