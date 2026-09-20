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

// HOLD on #1871 at 71604b4e (johnny-...): "The two named items refuse. On APFS,
// `EvalSymlinks("/Users/Glenn")` stays `/Users/Glenn` and string-compare misses
// `/Users/glenn`. Identify HOME by `os.SameFile`, not by the string."
//
// A directory has many names and EvalSymlinks does not reduce them to one: it resolves
// links and cleans, which is enough on a case-sensitive volume and is why every string
// compare in this package passed on linux. The volume the tool actually runs on for Glenn
// is APFS, where a case variant and a Unicode normalisation variant are the SAME
// DIRECTORY and compare unequal -- and neither folding the case nor picking a form is
// something the process can read off the path.
//
// NO TEST HERE EVER NAMES THE REAL HOME. Each one sets HOME to a directory it made under
// t.TempDir() and deletes only inside it.

// caseInsensitiveVolume probes, rather than assumes, whether dir's volume folds case: it
// makes one file and asks for it back by another spelling. It is a probe because the
// answer is the volume's, not the platform's -- a case-sensitive APFS volume exists, and
// so does a case-insensitive mount under linux.
func caseInsensitiveVolume(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "CaseProbe")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(probe)
	_, err := os.Stat(filepath.Join(dir, "caseprobe"))
	return err == nil
}

// A HOME THE STRING COMPARE MISSES, three spellings of it, and the removal each one used
// to get. Every case is the same shape: the path handed in IS the home directory, by
// device and inode, and the only question is whether the check can see that.
func TestRemoveUnderRootsIdentifiesTheHomeByIdentityNotBySpelling(t *testing.T) {
	// A RELATIVE HOME. os.UserHomeDir hands back $HOME exactly as it stands, and a
	// relative one resolves to a relative string that can never equal the absolute path
	// under test -- so the home comparison silently answered "different" about the home
	// itself. This is the portable case: it is red on linux and on darwin alike, and it
	// is the same bug Johnny found, met through the spelling instead of through the case.
	t.Run("HOME spelled relative to the working directory", func(t *testing.T) {
		parent := t.TempDir()
		realHome := filepath.Join(parent, "home")
		mustWrite(t, filepath.Join(realHome, "keep"), "x")
		t.Chdir(parent)
		t.Setenv("HOME", "home")

		err := RemoveUnderRoots(realHome, parent)
		if err == nil || !errors.Is(err, ErrUnsafe) {
			t.Errorf("RemoveUnderRoots(%q, %q) with HOME=%q = %v, want a refusal that wraps ErrUnsafe", realHome, parent, "home", err)
		}
		if !exists(realHome) {
			t.Errorf("RemoveUnderRoots deleted %s, the fixture's stand-in home, because HOME was spelled relative", realHome)
		}
	})

	// A HOME THE OLD CHECK COULD NOT RESOLVE. EvalSymlinks(home) returning an error made
	// the comparison vanish -- `if err == nil && ...` -- so the one case where the tool
	// could NOT tell whether it was about to delete the home was the case where it went
	// ahead. An unanswered question about the home is a refusal.
	t.Run("HOME that cannot be stat'd fails closed", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root traverses a 0o000 directory, so the unreadable-home case cannot be built here")
		}
		parent := t.TempDir()
		locked := filepath.Join(parent, "locked")
		hidden := filepath.Join(locked, "home")
		mustWrite(t, filepath.Join(hidden, "keep"), "x")
		victimRoot := t.TempDir()
		victim := filepath.Join(victimRoot, "victim")
		mustWrite(t, filepath.Join(victim, "keep"), "x")
		if err := os.Chmod(locked, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(locked, 0o755) })
		t.Setenv("HOME", hidden)

		err := RemoveUnderRoots(victim, victimRoot)
		if err == nil {
			t.Errorf("RemoveUnderRoots(%q, %q) = nil with an unreadable HOME; a home the check cannot identify is a refusal, not a pass", victim, victimRoot)
		}
		if !exists(victim) {
			t.Errorf("RemoveUnderRoots removed %s while it could not tell whether HOME was involved", victim)
		}
	})

	// JOHNNY'S OWN CASE, on the volume that has it. Skipped on hulk, which is linux and
	// case-sensitive; it is the Studio's leg of the PR's CI that runs this one.
	t.Run("a case-variant spelling of HOME", func(t *testing.T) {
		parent := t.TempDir()
		if !caseInsensitiveVolume(t, parent) {
			t.Skip("this volume is case-sensitive, so a case variant is a different directory here; the darwin CI leg covers this")
		}
		realHome := filepath.Join(parent, "glenn")
		mustWrite(t, filepath.Join(realHome, "keep"), "x")
		t.Setenv("HOME", realHome)
		variant := filepath.Join(parent, "Glenn")
		vi, verr := os.Stat(variant)
		ri, rerr := os.Stat(realHome)
		if verr != nil || rerr != nil || !os.SameFile(vi, ri) {
			t.Fatalf("the fixture's own premise failed: %q and %q are not the same directory", variant, realHome)
		}

		err := RemoveUnderRoots(variant, parent)
		if err == nil || !errors.Is(err, ErrUnsafe) {
			t.Errorf("RemoveUnderRoots(%q, %q) = %v, want a refusal: it IS %q", variant, parent, err, realHome)
		}
		if !exists(realHome) {
			t.Errorf("RemoveUnderRoots deleted the fixture's home through the spelling %q", variant)
		}
	})
}

// The same three questions of the single-root door, which has its own copy of the check.
func TestRemoveUnderIdentifiesTheHomeByIdentityNotBySpelling(t *testing.T) {
	t.Run("a relative HOME as the root", func(t *testing.T) {
		parent := t.TempDir()
		realHome := filepath.Join(parent, "home")
		victim := filepath.Join(realHome, "victim")
		mustWrite(t, filepath.Join(victim, "keep"), "x")
		t.Chdir(parent)
		t.Setenv("HOME", "home")

		err := RemoveUnder(realHome, victim)
		if err == nil || !errors.Is(err, ErrUnsafe) {
			t.Errorf("RemoveUnder(%q, %q) with HOME=%q = %v, want a refusal: the root IS the home", realHome, victim, "home", err)
		}
		if !exists(victim) {
			t.Errorf("RemoveUnder removed %s under a root that is the home, because HOME was spelled relative", victim)
		}
	})

	t.Run("a case-variant spelling of HOME as the root", func(t *testing.T) {
		parent := t.TempDir()
		if !caseInsensitiveVolume(t, parent) {
			t.Skip("this volume is case-sensitive, so a case variant is a different directory here; the darwin CI leg covers this")
		}
		realHome := filepath.Join(parent, "glenn")
		victim := filepath.Join(realHome, "victim")
		mustWrite(t, filepath.Join(victim, "keep"), "x")
		t.Setenv("HOME", realHome)

		err := RemoveUnder(filepath.Join(parent, "Glenn"), victim)
		if err == nil || !errors.Is(err, ErrUnsafe) {
			t.Errorf("RemoveUnder with the root spelled %q = %v, want a refusal: it IS the home", filepath.Join(parent, "Glenn"), err)
		}
		if !exists(victim) {
			t.Errorf("RemoveUnder removed %s under the home spelled with a different case", victim)
		}
	})
}

// The containment test is an ancestor relation and it is identity too: a path reached
// through a spelling of its root that the root's own string is not a prefix of is still
// under it, and a path that merely shares a prefix is not. The second half is what a
// string prefix gets wrong in the other direction.
func TestStrictlyUnderIsAnIdentityWalkNotAStringPrefix(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	deep := filepath.Join(root, "a", "b")
	mustWrite(t, filepath.Join(deep, "keep"), "x")
	sibling := filepath.Join(parent, "rootsibling")
	mustWrite(t, filepath.Join(sibling, "keep"), "x")

	// Reached through a symlinked PARENT: a different string for the same directory.
	link := filepath.Join(parent, "alias")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		root string
		path string
		want bool
	}{
		{"below, by the plain spelling", root, deep, true},
		{"below, with the root spelled through a symlink", link, deep, true},
		{"the root itself is not strictly below itself", root, root, false},
		{"a sibling whose name merely starts with the root's", root, sibling, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := strictlyUnder(c.root, c.path)
			if err != nil {
				t.Fatalf("strictlyUnder(%q, %q) errored: %v", c.root, c.path, err)
			}
			if got != c.want {
				t.Errorf("strictlyUnder(%q, %q) = %v, want %v", c.root, c.path, got, c.want)
			}
		})
	}
}
