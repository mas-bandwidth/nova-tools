package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoReplacePublishesOnlyAfterModeAndSync(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "file")
	h := defaultHooks()
	var calls []string
	chmod, sync, closeFile, link, remove, syncDir := h.chmod, h.sync, h.close, h.link, h.remove, h.syncDir
	h.chmod = func(f *os.File, m os.FileMode) error { calls = append(calls, "chmod"); return chmod(f, m) }
	h.sync = func(f *os.File) error {
		calls = append(calls, "sync")
		fi, err := f.Stat()
		if err != nil {
			return err
		}
		if runtime.GOOS != "windows" {
			assert.Equal(t, os.FileMode(0644), fi.Mode().Perm(), "mode at sync=%o", fi.Mode().Perm())
		}
		return sync(f)
	}
	h.close = func(f *os.File) error { calls = append(calls, "close"); return closeFile(f) }
	h.link = func(a, b string) error {
		calls = append(calls, "link")
		data, err := os.ReadFile(a)
		if err != nil {
			return err
		}
		assert.Equal(t, "complete", string(data), "published bytes=%q", data)
		return link(a, b)
	}
	h.remove = func(p string) error { calls = append(calls, "remove"); return remove(p) }
	h.syncDir = func(p string) error { calls = append(calls, "dir-sync"); return syncDir(p) }
	err := writeWithHooks(path, []byte("complete"), 0644, h, ExactMode(), NoReplace())
	require.NoError(t, err)
	want := []string{"chmod", "sync", "close", "link", "remove", "dir-sync"}
	require.Equal(t, want, calls, "calls=%v want=%v", calls, want)
	err = WriteFile(path, []byte("replacement"), 0600, NoReplace())
	require.ErrorIs(t, err, fs.ErrExist, "existing entry: %v", err)
	data, err := os.ReadFile(path)
	require.NoError(t, err, "retained bytes=%q err=%v", data, err)
	require.Equal(t, "complete", string(data), "retained bytes=%q err=%v", data, err)
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err, "entries=%v err=%v", entries, err)
	require.Len(t, entries, 1, "entries=%v err=%v", entries, err)
}

func TestNoReplacePreservesConcurrentWinnerAndReportsFailures(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"concurrent winner", "link fails", "cleanup fails"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "file")
			h := defaultHooks()
			boom := errors.New("injected failure")
			switch mode {
			case "concurrent winner":
				h.link = func(a, b string) error {
					if err := os.WriteFile(b, []byte("winner"), 0600); err != nil {
						return err
					}
					return os.Link(a, b)
				}
			case "link fails":
				h.link = func(a, b string) error { return boom }
			case "cleanup fails":
				h.remove = func(string) error { return boom }
			}
			synced := false
			h.syncDir = func(string) error { synced = true; return nil }
			err := writeWithHooks(path, []byte("complete"), 0644, h, NoReplace())
			wantErr := boom
			if mode == "concurrent winner" {
				wantErr = fs.ErrExist
			}
			require.ErrorIs(t, err, wantErr, "error=%v want=%v", err, wantErr)
			data, readErr := os.ReadFile(path)
			wantEntries := 0
			switch mode {
			case "concurrent winner":
				wantEntries = 1
				require.NoError(t, readErr, "winner=%q err=%v", data, readErr)
				require.Equal(t, "winner", string(data), "winner=%q err=%v", data, readErr)
			case "link fails":
				require.ErrorIs(t, readErr, fs.ErrNotExist, "failed publication left target: %v", readErr)
			case "cleanup fails":
				wantEntries = 2
				require.NoError(t, readErr, "published=%q err=%v dirSynced=%v", data, readErr, synced)
				require.Equal(t, "complete", string(data), "published=%q err=%v dirSynced=%v", data, readErr, synced)
				require.True(t, synced, "published=%q err=%v dirSynced=%v", data, readErr, synced)
				require.Contains(t, err.Error(), "created", "error hides published state: %v", err)
				require.Contains(t, err.Error(), "cleanup failed", "error hides published state: %v", err)
			}
			entries, e := os.ReadDir(dir)
			require.NoError(t, e, "entries=%v err=%v want=%d", entries, e, wantEntries)
			require.Len(t, entries, wantEntries, "entries=%v err=%v want=%d", entries, e, wantEntries)
		})
	}
}
