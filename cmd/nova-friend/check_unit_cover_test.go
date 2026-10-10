package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/require"
)

// TestNovaFriendCheckCoverShownMissingFile pins --shown naming a missing file:
// exit 2 and a refusal that says the file cannot be read.
func TestNovaFriendCheckCoverShownMissingFile(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	missing := filepath.Join(t.TempDir(), "missing.json")
	r.cli().Do(t, "check", "--as", "ada", "--shown", missing, "bob").Exit(2).
		Err("cannot be read")
}

// TestNovaFriendCheckCoverShownInvalidJSON pins --shown naming a file that is
// not JSON: exit 2 and a refusal that says so.
func TestNovaFriendCheckCoverShownInvalidJSON(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	shown := filepath.Join(t.TempDir(), "shown.json")
	require.NoError(t, os.WriteFile(shown, []byte(`{ invalid json }`), 0o644))
	r.cli().Do(t, "check", "--as", "ada", "--shown", shown, "bob").Exit(2).
		Err("is invalid JSON")
}

// TestNovaFriendCheckCoverShownStdin pins --shown - reading the map on stdin:
// the VERDICT line carries the shown state it read.
func TestNovaFriendCheckCoverShownStdin(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.cli().DoIn(t, `{"bob":{"state":"up","working":1}}`, "check", "--as", "ada", "--shown", "-", "bob").Exit(1).
		Out("CHECK VERDICT friend=bob verdict=down shown=up/1")
}

// TestNovaFriendCheckCoverStoreFail pins a store that does not open: exit 2,
// the store's error, and no CHECK line printed.
func TestNovaFriendCheckCoverStoreFail(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.store.Fail = errors.New("store is down")
	ran := r.cli().Do(t, "check", "--as", "ada", "bob").Exit(2)
	require.Contains(t, ran.Stderr, "store is down")
	require.NotContains(t, ran.Stdout, "CHECK")
}

// TestNovaFriendCheckCoverNoPosFriendFromStore pins a run with no positional
// friend whose list comes from the store's friends: bob is checked once.
func TestNovaFriendCheckCoverNoPosFriendFromStore(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.store.Friends = []string{"bob"}
	r.cli().Do(t, "check", "--as", "ada").Exit(1).Out("CHECK DAEMON friend=bob", "friends=1")
}

// TestNovaFriendCheckCoverNoPosFriendFromFS pins a run with no positional
// friend and no store: the directories under the home's .nova-friend are the
// list, a plain file there is not, and the list is sorted.
func TestNovaFriendCheckCoverNoPosFriendFromFS(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada")
	r.env = map[string]string{"PATH": "/usr/bin:/bin"}
	base := filepath.Join(r.home, ".nova-friend")
	require.NoError(t, os.MkdirAll(filepath.Join(base, "zed"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "amy"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(base, "plain"), []byte("x"), 0o644))
	ran := r.cli().Do(t, "check", "--as", "ada").Exit(1).
		Out("friends=2", "CHECK DAEMON friend=amy", "CHECK DAEMON friend=zed")
	require.Less(t, strings.Index(ran.Stdout, "CHECK DAEMON friend=amy"), strings.Index(ran.Stdout, "CHECK DAEMON friend=zed"),
		"the filesystem list is sorted, amy before zed")
}

// TestNovaFriendCheckCoverNoFriends pins a run with no positional friend, no
// store and no .nova-friend directory: no friend is checked, exit 0.
func TestNovaFriendCheckCoverNoFriends(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada")
	r.env = map[string]string{"PATH": "/usr/bin:/bin"}
	r.cli().Do(t, "check", "--as", "ada").Exit(0).Out("CHECK OK friends=0")
}

// TestNovaFriendCheckCoverStateDirSubdir pins --state-dir D where the friend's
// own subdirectory D/bob holds the status: the HARNESS line names its harness.
func TestNovaFriendCheckCoverStateDirSubdir(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	state := t.TempDir()
	bob := filepath.Join(state, "bob")
	require.NoError(t, os.MkdirAll(bob, 0o755))
	require.NoError(t, friend.WriteStatus(bob, friend.Status{
		Friend: "bob", Harness: "opencode", At: start, Connection: friend.Connected,
	}))
	r.cli().Do(t, "check", "--as", "ada", "--state-dir", state, "bob").Exit(1).
		Out("CHECK HARNESS friend=bob harness=opencode")
}

// TestNovaFriendCheckCoverStateDirSelf pins --state-dir D where D itself (and
// no per-friend subdirectory) holds the status: the HARNESS line names its
// harness.
func TestNovaFriendCheckCoverStateDirSelf(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	state := t.TempDir()
	require.NoError(t, friend.WriteStatus(state, friend.Status{
		Friend: "bob", Harness: "opencode", At: start, Connection: friend.Connected,
	}))
	r.cli().Do(t, "check", "--as", "ada", "--state-dir", state, "bob").Exit(1).
		Out("CHECK HARNESS friend=bob harness=opencode")
}

// TestNovaFriendCheckCoverPlist pins the fallback with no --state-dir: the
// daemon's plist names the state directory, and the status written there is
// the one read.
func TestNovaFriendCheckCoverPlist(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	state := t.TempDir()
	agent := friend.Agent{Friend: "bob", Home: r.home, StateDir: state}
	require.NoError(t, os.MkdirAll(filepath.Dir(agent.PlistPath()), 0o755))
	require.NoError(t, os.WriteFile(agent.PlistPath(), []byte(agent.Plist()), 0o644))
	require.NoError(t, friend.WriteStatus(state, friend.Status{
		Friend: "bob", Harness: "opencode", At: start, Connection: friend.Connected,
	}))
	r.cli().Do(t, "check", "--as", "ada", "bob").Exit(1).
		Out("CHECK HARNESS friend=bob harness=opencode")
}

// TestNovaFriendCheckCoverDir pins --dir W feeding the work reader: W/inbox
// holds two files and the WORK line counts them.
func TestNovaFriendCheckCoverDir(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	work := t.TempDir()
	inbox := filepath.Join(work, "inbox")
	require.NoError(t, os.MkdirAll(inbox, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(inbox, "a"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(inbox, "b"), []byte("y"), 0o644))
	r.cli().Do(t, "check", "--as", "ada", "--dir", work, "bob").Exit(1).
		Out("CHECK WORK friend=bob inbox=2")
}

// TestNovaFriendCheckCoverArgAfter pins the value after a flag: the last word
// with no value, an absent flag and a present flag.
func TestNovaFriendCheckCoverArgAfter(t *testing.T) {
	t.Parallel()
	require.Equal(t, "", argAfter([]string{"--flag", "--last"}, "--last"))
	require.Equal(t, "", argAfter([]string{"--flag"}, "--last"))
	require.Equal(t, "value", argAfter([]string{"--flag", "--last", "value"}, "--last"))
}
