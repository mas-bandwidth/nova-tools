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

func TestRemoveUnderRootsRefusesARootThatIsASymlinkToHome(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	fakeHomeReal, err := filepath.EvalSymlinks(fakeHome)
	if err != nil {
		t.Fatalf("could not resolve the fixture's fake home: %v", err)
	}
	victim := filepath.Join(fakeHomeReal, "victim")
	mustWrite(t, filepath.Join(victim, "keep"), "x")

	elsewhere := t.TempDir()
	rootLink := filepath.Join(elsewhere, "home-link")
	if err := os.Symlink(fakeHomeReal, rootLink); err != nil {
		t.Fatal(err)
	}

	err = RemoveUnderRoots(victim, rootLink)
	if err == nil || !errors.Is(err, ErrUnsafe) {
		t.Errorf("RemoveUnderRoots(%q, %q) = %v, want a refusal that wraps ErrUnsafe", victim, rootLink, err)
	}
	if !exists(victim) {
		t.Errorf("RemoveUnderRoots deleted %s through a root that is a symlink to the fixture's home", victim)
	}
}

func TestRemoveUnderRootsRefusesAPathThatIsHomeUnderAWiderRoot(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	fakeHomeReal, err := filepath.EvalSymlinks(fakeHome)
	if err != nil {
		t.Fatalf("could not resolve the fixture's fake home: %v", err)
	}
	marker := filepath.Join(fakeHomeReal, "marker")
	mustWrite(t, marker, "x")

	parent := filepath.Dir(fakeHomeReal)

	err = RemoveUnderRoots(fakeHomeReal, parent)
	if err == nil || !errors.Is(err, ErrUnsafe) {
		t.Errorf("RemoveUnderRoots(%q, %q) = %v, want a refusal that wraps ErrUnsafe", fakeHomeReal, parent, err)
	}
	if !exists(marker) {
		t.Errorf("RemoveUnderRoots deleted %s, the fixture's stand-in home, under a wider root", fakeHomeReal)
	}
}

// RemoveUnder resolves its root through symlinks for the containment test but never
// re-asks whether the RESOLVED root is a boundary at all: refuseUnsafeRoot sees only
// the absolute form. So a root that is a symlink to the home, or to the whole disk,
// walks past the check that a literal home or "/" is refused by, and the removal then
// happens under the real home. Johnny, 2026-09-19: "HOME as a literal root refuses. A
// symlink to HOME, and HOME as a path under /Users, do not." Both doors must refuse the
// resolved root, not the name the caller happened to spell it with.
func TestRemoveUnderRefusesARootThatResolvesToAnUnsafeDirectory(t *testing.T) {
	t.Run("a root that is a symlink to the home", func(t *testing.T) {
		fakeHome := t.TempDir()
		t.Setenv("HOME", fakeHome)
		fakeHomeReal, err := filepath.EvalSymlinks(fakeHome)
		if err != nil {
			t.Fatalf("could not resolve the fixture's fake home: %v", err)
		}
		victim := filepath.Join(fakeHomeReal, "victim")
		mustWrite(t, filepath.Join(victim, "keep"), "x")

		elsewhere := t.TempDir()
		rootLink := filepath.Join(elsewhere, "home-link")
		if err := os.Symlink(fakeHomeReal, rootLink); err != nil {
			t.Fatal(err)
		}

		err = RemoveUnder(rootLink, victim)
		if err == nil || !errors.Is(err, ErrUnsafe) {
			t.Errorf("RemoveUnder(%q, %q) = %v, want a refusal that wraps ErrUnsafe", rootLink, victim, err)
		}
		if !exists(victim) {
			t.Errorf("RemoveUnder deleted %s through a root that is a symlink to the fixture's home", victim)
		}
	})

	t.Run("a root that is a symlink to the whole disk", func(t *testing.T) {
		elsewhere := t.TempDir()
		rootLink := filepath.Join(elsewhere, "disk-link")
		if err := os.Symlink(string(os.PathSeparator), rootLink); err != nil {
			t.Fatal(err)
		}
		victim := filepath.Join(t.TempDir(), "victim")
		mustWrite(t, filepath.Join(victim, "keep"), "x")

		err := RemoveUnder(rootLink, victim)
		if err == nil || !errors.Is(err, ErrUnsafe) {
			t.Errorf("RemoveUnder(%q, %q) = %v, want a refusal that wraps ErrUnsafe", rootLink, victim, err)
		}
		if !exists(victim) {
			t.Errorf("RemoveUnder deleted %s through a root that is a symlink to the whole disk", victim)
		}
	})
}
