package safepath

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// This package deletes directories, so its whole job is to refuse a removal whose
// boundary is not a boundary. RemoveUnder checks its root; RemoveUnderRoots did not.
// RemoveUnderRoots is the hygiene reaper's door, and its roots are caller-supplied:
// they are literals under --home by default, but --roots overrides them and --home
// is any absolute path. So it is the one door whose root a caller can choose, and it
// was the one door without a check. A cold reader probed it and removed a victim
// with a home-directory root, unrefused. Every path this test touches is one the
// test itself created under t.TempDir() or under an os.MkdirTemp it removes in
// t.Cleanup. The third subtest exists because the obvious fix -- returning on the
// first unsafe root -- would break a caller who names several.
func TestRemoveUnderRootsRefusesAnUnsafeRoot(t *testing.T) {
	home, err := os.UserHomeDir()

	t.Run("the user's home", func(t *testing.T) {
		if err != nil || home == "" {
			t.Skipf("this machine has no home directory to test the refusal against: %v", err)
		}
		parent, err := os.MkdirTemp(home, "safepath-unsafe-root-")
		if err != nil {
			t.Fatalf("could not make a directory under the home: %v", err)
		}
		t.Cleanup(func() { os.RemoveAll(parent) })
		victim := filepath.Join(parent, "victim")
		mustWrite(t, filepath.Join(victim, "keep"), "x")

		err = RemoveUnderRoots(victim, home)
		if err == nil || !errors.Is(err, ErrUnsafe) {
			t.Errorf("RemoveUnderRoots(%q, %q) = %v, want a refusal that wraps ErrUnsafe", victim, home, err)
		}
		if !exists(victim) {
			t.Errorf("RemoveUnderRoots deleted %s under the user's home; a refusal must leave it", victim)
		}
	})

	t.Run("the whole disk", func(t *testing.T) {
		root := t.TempDir()
		victim := filepath.Join(root, "victim")
		mustWrite(t, filepath.Join(victim, "keep"), "x")

		err := RemoveUnderRoots(victim, string(os.PathSeparator))
		if err == nil || !errors.Is(err, ErrUnsafe) {
			t.Errorf("RemoveUnderRoots(%q, %q) = %v, want a refusal that wraps ErrUnsafe", victim, string(os.PathSeparator), err)
		}
		if !exists(victim) {
			t.Errorf("RemoveUnderRoots deleted %s under the whole disk; a refusal must leave it", victim)
		}
	})

	t.Run("a safe root beside an unsafe one still works", func(t *testing.T) {
		if err != nil || home == "" {
			t.Skipf("this machine has no home directory to test the refusal against: %v", err)
		}
		root := t.TempDir()
		victim := filepath.Join(root, "victim")
		mustWrite(t, filepath.Join(victim, "keep"), "x")

		if err := RemoveUnderRoots(victim, home, root); err != nil {
			t.Fatalf("RemoveUnderRoots(%q, %q, %q) = %v, want nil; a safe root beside an unsafe one must still remove", victim, home, root, err)
		}
		if exists(victim) {
			t.Fatalf("RemoveUnderRoots left %s behind even though %q is a safe root", victim, root)
		}
	})
}
