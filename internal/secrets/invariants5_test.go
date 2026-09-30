package secrets

import (
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
