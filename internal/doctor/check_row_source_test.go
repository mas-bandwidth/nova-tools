package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHarnessAndFriendSeatNameTheDirectoryOrUnitTheyRead pins the source each
// check names in its evidence (docs/SPEC-DOCTOR.md, "the friend checks"): the
// harness check the directory it read, the friend-seat check the units it
// counted, so the two answers cannot contradict unnamed.
func TestHarnessAndFriendSeatNameTheDirectoryOrUnitTheyRead(t *testing.T) {
	t.Parallel()

	t.Run("harness names the empty directory it read", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		friends := filepath.Join(root, "friends")
		require.NoError(t, os.MkdirAll(friends, 0o755))
		r := checkHarness(context.Background(), fakeEnv{env: map[string]string{
			"NOVA_FRIEND_HOME": friends,
			"PATH":             filepath.Join(root, "bin"),
		}})
		assert.Equal(t, OK, r.Status)
		assert.Contains(t, r.Evidence, friends)
	})

	t.Run("harness names the missing directory it read", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		missing := filepath.Join(root, "no-friends")
		r := checkHarness(context.Background(), fakeEnv{env: map[string]string{
			"NOVA_FRIEND_HOME": missing,
			"PATH":             filepath.Join(root, "bin"),
		}})
		assert.Equal(t, OK, r.Status)
		assert.Contains(t, r.Evidence, missing)
	})

	t.Run("friend-seat names the unit label it counted", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFriendPlist(t, root, "ada", "--as", "ada", "--coordinator", "new", "--harness", "claude")
		env := friendEnv{goos: "darwin", fakeEnv: fakeEnv{
			env:  map[string]string{"HOME": "home"},
			root: root,
			exec: func(name string, _ ...string) (string, error) {
				if name == "nova-sprint" {
					return `{"coordinator":"new"}`, nil
				}
				return "", errors.New("unexpected " + name)
			},
		}}
		r := checkFriendSeat(context.Background(), env)
		assert.Equal(t, OK, r.Status)
		assert.Contains(t, r.Evidence, "com.nova.friend-ada")
	})

	t.Run("friend-seat names the unit pattern and directory it read", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		env := friendEnv{goos: "darwin", fakeEnv: fakeEnv{
			env:  map[string]string{"HOME": "home"},
			root: root,
		}}
		r := checkFriendSeat(context.Background(), env)
		assert.Equal(t, OK, r.Status)
		assert.Contains(t, r.Evidence, "com.nova.friend-")
		assert.Contains(t, r.Evidence, filepath.Join("home", "Library", "LaunchAgents"))
	})

	t.Run("friend-seat names the systemd unit label on linux", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFriendUnit(t, root, "ada", "--as", "ada", "--coordinator", "new", "--harness", "claude")
		env := friendEnv{goos: "linux", fakeEnv: fakeEnv{
			env:  map[string]string{"HOME": "home"},
			root: root,
			exec: func(name string, _ ...string) (string, error) {
				if name == "nova-sprint" {
					return `{"coordinator":"new"}`, nil
				}
				return "", errors.New("unexpected " + name)
			},
		}}
		r := checkFriendSeat(context.Background(), env)
		assert.Equal(t, OK, r.Status)
		assert.Contains(t, r.Evidence, "nova-friend-ada")
	})
}