package fuse

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestCreateBoxRefusesSymlinkParent(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink privilege is not available on all Windows runners")
	}
	root := t.TempDir()
	real := filepath.Join(root, "real")
	link := filepath.Join(root, "link")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := CreateBox(filepath.Join(link, "box.json")); err == nil {
		t.Error("CreateBox accepted a symlink parent")
	}
	entries, err := os.ReadDir(real)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("refusal left entries in the linked directory: %v", entries)
	}
}

func TestConcurrentCreateBoxHasOneWinner(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "box.json")
	var wg sync.WaitGroup
	var wins atomic.Int32
	for range 16 {
		wg.Go(func() {
			err := CreateBox(path)
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, fs.ErrExist) {
				t.Errorf("create: %v", err)
			}
		})
	}
	wg.Wait()
	if n := wins.Load(); n != 1 {
		t.Fatalf("successful creators=%d, want1", n)
	}
	box, err := ReadBox(path)
	if err != nil {
		t.Fatal(err)
	}
	if box.Lockdown != nil || len(box.Quarantine) != 0 {
		t.Fatalf("created box not empty: %+v", box)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("creation left temp files: %v", entries)
	}
}
