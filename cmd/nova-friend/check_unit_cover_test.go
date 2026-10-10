package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/require"
)

// TestNovaFriendCheckCoverShownMissingFile tests --shown naming a missing file (exit 2, "cannot be read")
func TestNovaFriendCheckCoverShownMissingFile(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	cli.Do(t, "check", "--as", "ada", "--shown", "/nonexistent/file.json", "bob").Exit(2).
		Err("cannot be read")
}

// TestNovaFriendCheckCoverShownInvalidJSON tests --shown naming a file of invalid JSON (exit 2, "is invalid JSON")
func TestNovaFriendCheckCoverShownInvalidJSON(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	shownFile := filepath.Join(t.TempDir(), "shown.json")
	require.NoError(t, os.WriteFile(shownFile, []byte(`{ invalid json }`), 0o644))
	cli.Do(t, "check", "--as", "ada", "--shown", shownFile, "bob").Exit(2).
		Err("is invalid JSON")
}

// TestNovaFriendCheckCoverShownStdin tests --shown - with stdin input
func TestNovaFriendCheckCoverShownStdin(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	ran := cli.RunIn(`{"bob":{"state":"up","working":1}}`, "check", "--as", "ada", "--shown", "-", "bob")
	require.Equal(t, 1, ran.Code)
	require.Contains(t, ran.Stdout, "VERDICT")
	require.Contains(t, ran.Stdout, "shown=up/1")
}

// TestNovaFriendCheckCoverStoreFail tests r.store.Fail set (exit 2, the store's error, no CHECK line)
func TestNovaFriendCheckCoverStoreFail(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.store.Fail = errors.New("store is down")
	cli := r.cli()
	ran := cli.Do(t, "check", "--as", "ada", "bob").Exit(2)
	require.Contains(t, ran.Stderr, "store is down")
	require.NotContains(t, ran.Stdout, "CHECK")
}

// TestNovaFriendCheckCoverNoPosFriendFromStore tests no positional friend with r.store.Friends = ["bob"]
func TestNovaFriendCheckCoverNoPosFriendFromStore(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	// Ensure store.Friends is set so ListFriends returns from store
	r.store.Friends = []string{"bob"}
	cli := r.cli()
	state := friend.DefaultStateDir(r.home, "bob")
	require.NoError(t, friend.WriteStatus(state, friend.Status{
		Friend:     "bob",
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
	require.NoError(t, friend.Record(state, "2026-10-04T02:50:00Z subject=work exit=0"))
	cli.Do(t, "check", "--as", "ada").Exit(1).
		Out("friends=1")
}

// TestNovaFriendCheckCoverNoPosFriendFromFS tests no positional friend, no store, directories zed and amy
func TestNovaFriendCheckCoverNoPosFriendFromFS(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada")
	r.env = map[string]string{"PATH": "/usr/bin:/bin"}
	home := r.home
	base := filepath.Join(home, ".nova-friend")
	require.NoError(t, os.MkdirAll(base, 0o755))
	// Create directories for zed and amy
	require.NoError(t, os.MkdirAll(filepath.Join(base, "zed"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "amy"), 0o755))
	// Create a plain file (should be ignored)
	require.NoError(t, os.WriteFile(filepath.Join(base, "not-a-dir"), []byte("x"), 0o644))
	cli := r.cli()
	// No friends in store, so should use filesystem
	// Should find amy and zed (sorted: amy first, then zed)
	ran := cli.Do(t, "check", "--as", "ada")
	require.Equal(t, 1, ran.Code) // friends are down
	require.Contains(t, ran.Stdout, "friends=2")
}

// TestNovaFriendCheckCoverNoFriends tests no positional friend, no store, no .nova-friend directory
func TestNovaFriendCheckCoverNoFriends(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada")
	r.env = map[string]string{"PATH": "/usr/bin:/bin"}
	// No .nova-friend directory
	cli := r.cli()
	ran := cli.Do(t, "check", "--as", "ada").Exit(0)
	require.Contains(t, ran.Stdout, "friends=0")
}

// TestNovaFriendCheckCoverStateDirSubdir tests --state-dir D where D/bob holds a status
func TestNovaFriendCheckCoverStateDirSubdir(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	state := t.TempDir()
	bobState := filepath.Join(state, "bob")
	require.NoError(t, os.MkdirAll(bobState, 0o755))
	// Write status using friend.WriteStatus
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
	require.NoError(t, friend.Record(bobState, "2026-10-04T02:50:00Z subject=work exit=0"))
	ran := cli.Do(t, "check", "--as", "ada", "--state-dir", state, "bob").Exit(1)
	require.Contains(t, ran.Stdout, "harness=opencode")
}

// TestNovaFriendCheckCoverStateDirSelf tests --state-dir D where only D itself holds status
func TestNovaFriendCheckCoverStateDirSelf(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	state := t.TempDir()
	// Write status directly in state dir (not in subdirectory)
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
	require.NoError(t, friend.Record(state, "2026-10-04T02:50:00Z subject=work exit=0"))
	ran := cli.Do(t, "check", "--as", "ada", "--state-dir", state, "bob").Exit(1)
	require.Contains(t, ran.Stdout, "harness=opencode")
}

// TestNovaFriendCheckCoverPlist tests plist with --state-dir
func TestNovaFriendCheckCoverPlist(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	home := r.home
	stateDir := t.TempDir()
	agent := friend.Agent{
		Friend:   "bob",
		Home:     home,
		StateDir: stateDir,
	}
	plistPath := agent.PlistPath()
	require.NoError(t, os.MkdirAll(filepath.Dir(plistPath), 0o755))
	require.NoError(t, os.WriteFile(plistPath, []byte(agent.Plist()), 0o644))
	// Write status under stateDir (this friend has no pre-existing status in rig)
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
	require.NoError(t, friend.Record(stateDir, "2026-10-04T02:50:00Z subject=work exit=0"))
	ran := cli.Do(t, "check", "--as", "ada", "bob")
	require.Equal(t, 0, ran.Code)
	require.Contains(t, ran.Stdout, "harness=opencode")
}

// TestNovaFriendCheckCoverDir tests --dir W where W/inbox holds two files
func TestNovaFriendCheckCoverDir(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	workDir := t.TempDir()
	inbox := filepath.Join(workDir, "inbox")
	require.NoError(t, os.MkdirAll(inbox, 0o755))
	// Create two files in inbox
	require.NoError(t, os.WriteFile(filepath.Join(inbox, "file1"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(inbox, "file2"), []byte("y"), 0o644))
	ran := cli.Do(t, "check", "--as", "ada", "--dir", workDir, "bob").Exit(1)
	require.Contains(t, ran.Stdout, "inbox=2")
}

// TestNovaFriendCheckCoverArgAfter tests argAfter function
func TestNovaFriendCheckCoverArgAfter(t *testing.T) {
	t.Parallel()
	// Test with flag last and no value (returns "")
	require.Equal(t, "", argAfter([]string{"--flag", "--last"}, "--last"))
	// Test with flag absent (returns "")
	require.Equal(t, "", argAfter([]string{"--flag"}, "--last"))
	// Test with flag present and value
	require.Equal(t, "value", argAfter([]string{"--flag", "--last", "value"}, "--last"))
}
