package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCheckInvariant5DotDotNamedDirIsInsideTheStore: a key file under a
// directory named "..cache" inside the store is refused, while a key outside
// the store (reached by "../x") is not reported as inside it.
func TestCheckInvariant5DotDotNamedDirIsInsideTheStore(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := filepath.Join(root, "store")
	inside := func(keyPath string) bool {
		for _, f := range CheckInvariant5(store, keyPath) {
			if f.Kind == "store-private-key" {
				return true
			}
		}
		return false
	}
	if !inside(filepath.Join(store, "..cache", "key.txt")) {
		t.Error("a key under store/..cache was not reported as inside the store")
	}
	if !inside(filepath.Join(store, "sub", "key.txt")) {
		t.Error("a key under store/sub was not reported as inside the store")
	}
	if inside(filepath.Join(store, "..", "x")) {
		t.Error("store/../x was reported as inside the store")
	}
	if inside(filepath.Join(root, "key.txt")) {
		t.Error("a key beside the store was reported as inside it")
	}
}

// TestCheckInvariant5FollowsSymlinks: a key is inside the store by where it lands. A key
// file that is a link into the store, a key under a directory that is a link into the
// store, and a key spelled through a link to the store itself are all inside it; a key
// that only has a link to somewhere else in its path is not.
func TestCheckInvariant5FollowsSymlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := filepath.Join(root, "store")
	other := filepath.Join(root, "other")
	for _, d := range []string{filepath.Join(store, "sub"), other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, name string) string {
		p := filepath.Join(root, name)
		if err := os.Symlink(target, p); err != nil {
			t.Skipf("symlinks are not available here: %v", err)
		}
		return p
	}
	inside := func(keyPath, storeDir string) bool {
		for _, f := range CheckInvariant5(storeDir, keyPath) {
			if f.Kind == "store-private-key" && f.Reason == "key file is inside store directory" {
				return true
			}
		}
		return false
	}
	file := filepath.Join(store, "sub", "key.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fileLink := link(file, "key-link")
	dirLink := link(filepath.Join(store, "sub"), "sub-link")
	storeLink := link(store, "store-link")
	otherLink := link(other, "other-link")

	if !inside(fileLink, store) {
		t.Error("a key file that is a link into the store was not inside it")
	}
	if !inside(filepath.Join(dirLink, "absent.txt"), store) {
		t.Error("a key under a directory that is a link into the store was not inside it")
	}
	if !inside(file, storeLink) {
		t.Error("a key in the store was not inside it when the store was named through a link")
	}
	if inside(filepath.Join(otherLink, "key.txt"), store) {
		t.Error("a key under a link to somewhere else was reported inside the store")
	}
	if inside(filepath.Join(other, "key.txt"), store) {
		t.Error("a key beside the store was reported inside it")
	}
}
