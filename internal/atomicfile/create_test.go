package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
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
		if runtime.GOOS != "windows" && fi.Mode().Perm() != 0644 {
			t.Errorf("mode at sync=%o", fi.Mode().Perm())
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
		if string(data) != "complete" {
			t.Errorf("published bytes=%q", data)
		}
		return link(a, b)
	}
	h.remove = func(p string) error { calls = append(calls, "remove"); return remove(p) }
	h.syncDir = func(p string) error { calls = append(calls, "dir-sync"); return syncDir(p) }
	if err := writeWithHooks(path, []byte("complete"), 0644, h, ExactMode(), NoReplace()); err != nil {
		t.Fatal(err)
	}
	want := []string{"chmod", "sync", "close", "link", "remove", "dir-sync"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v want=%v", calls, want)
	}
	if err := WriteFile(path, []byte("replacement"), 0600, NoReplace()); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("existing entry: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "complete" {
		t.Fatalf("retained bytes=%q err=%v", data, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
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
			if !errors.Is(err, wantErr) {
				t.Fatalf("error=%v want=%v", err, wantErr)
			}
			data, readErr := os.ReadFile(path)
			wantEntries := 0
			switch mode {
			case "concurrent winner":
				wantEntries = 1
				if readErr != nil || string(data) != "winner" {
					t.Fatalf("winner=%q err=%v", data, readErr)
				}
			case "link fails":
				if !errors.Is(readErr, fs.ErrNotExist) {
					t.Fatalf("failed publication left target: %v", readErr)
				}
			case "cleanup fails":
				wantEntries = 2
				if readErr != nil || string(data) != "complete" || !synced {
					t.Fatalf("published=%q err=%v dirSynced=%v", data, readErr, synced)
				}
				if !strings.Contains(err.Error(), "created") || !strings.Contains(err.Error(), "cleanup failed") {
					t.Fatalf("error hides published state: %v", err)
				}
			}
			entries, e := os.ReadDir(dir)
			if e != nil || len(entries) != wantEntries {
				t.Fatalf("entries=%v err=%v want=%d", entries, e, wantEntries)
			}
		})
	}
}
