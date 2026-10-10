package friend

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFriendStateCoverStateDirIn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		dir  string
		want string
	}{
		{"/home/user", "/home/user/.nova-friend"},
		{"data", "data/.nova-friend"},
	}

	for _, tt := range tests {
		t.Run(tt.dir, func(t *testing.T) {
			t.Parallel()
			got := StateDirIn(tt.dir)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFriendStateCoverDaemonStateDir(t *testing.T) {
	t.Parallel()

	t.Run("empty dir", func(t *testing.T) {
		t.Parallel()
		got, _ := DaemonStateDir("", nil)
		expected, _ := DefaultStateDir(os.Getenv("HOME"), "friend")
		assert.Equal(t, expected, got)
	})

	t.Run("mkdir succeeds", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		made := false
		mkdir := func(p string) error {
			made = true
			return nil
		}
		got, reason := DaemonStateDir(dir, mkdir)
		assert.Equal(t, StateDirIn(dir), got)
		assert.Equal(t, "", reason)
		assert.True(t, made)
	})

	t.Run("mkdir fails", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		err := errors.New("mkdir failed")
		mkdir := func(p string) error {
			return err
		}
		got, _ := DaemonStateDir(dir, mkdir)
		expected, _ := DefaultStateDir("", "friend")
		assert.Equal(t, expected, got)
	})
}

func TestFriendStateCoverFindStateDir(t *testing.T) {
	t.Parallel()

	t.Run("status.json exists", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		sd := StateDirIn(dir)
		require.NoError(t, os.MkdirAll(sd, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(sd, "status.json"), []byte("{}"), 0o600))
		got, _ := FindStateDir(dir)
		assert.Equal(t, sd, got)
	})

	t.Run("no status file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		got, _ := FindStateDir(dir)
		expected, _ := DefaultStateDir(os.Getenv("HOME"), "friend")
		assert.Equal(t, expected, got)
	})

	t.Run("empty dir", func(t *testing.T) {
		t.Parallel()
		got, _ := FindStateDir("")
		expected, _ := DefaultStateDir(os.Getenv("HOME"), "friend")
		assert.Equal(t, expected, got)
	})
}

func TestFriendStateCoverWatchPath(t *testing.T) {
	t.Parallel()

	got := WatchPath("/state/dir")
	assert.Equal(t, "/state/dir/watch.json", got)
}

func TestFriendStateCoverWriteReadWatch(t *testing.T) {
	t.Parallel()

	t.Run("round-trip", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := WatchPath(dir)
		w := Watch{After: "123", WakeOffset: 456}
		require.NoError(t, WriteWatch(path, w))
		got, found, err := ReadWatch(path)
		assert.True(t, found)
		assert.NoError(t, err)
		assert.Equal(t, w, got)
	})

	t.Run("missing directory", func(t *testing.T) {
		t.Parallel()
		got, found, err := ReadWatch(filepath.Join(t.TempDir(), "missing", "watch.json"))
		assert.False(t, found)
		assert.NoError(t, err)
		assert.Equal(t, Watch{}, got)
	})

	t.Run("not json", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := WatchPath(dir)
		require.NoError(t, os.WriteFile(path, []byte("not json"), 0o600))
		got, found, err := ReadWatch(path)
		assert.True(t, found)
		assert.Error(t, err)
		assert.Equal(t, Watch{}, got)
	})

	t.Run("write beneath regular file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// Create a regular file at the stateDir path
		regular := filepath.Join(dir, "not-a-dir")
		require.NoError(t, os.WriteFile(regular, []byte("regular"), 0o600))
		err := WriteWatch(filepath.Join(regular, "watch.json"), Watch{After: "123"})
		assert.Error(t, err)
	})
}
