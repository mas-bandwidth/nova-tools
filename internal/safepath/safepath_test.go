package safepath

import (
	"os"
	"path/filepath"
	"testing"
)

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// name-ok-is-one-path-element: only [A-Za-z0-9._-]+, never empty, never ".",
// never "..", never a slash and never a leading dash.
func TestNameOKIsOnePathElement(t *testing.T) {
	t.Parallel()

	ok := []string{"3", "card-9322", "a.b_c-d", "..hidden", "x..y"}
	for _, s := range ok {
		if !NameOK(s) {
			t.Errorf("NameOK(%q) = false, want true", s)
		}
	}
	bad := []string{"", ".", "..", "../escape", "a/b", "-flag", "two words", "tab\t"}
	for _, s := range bad {
		if NameOK(s) {
			t.Errorf("NameOK(%q) = true, want false", s)
		}
	}
}

// remove-under-is-the-only-rm: a directory strictly below a root goes.
func TestRemoveUnderRootsRemovesBelowRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	victim := filepath.Join(root, "slot", "jobs", "card-1")
	mustWrite(t, filepath.Join(victim, "scratch", "s"), "x")
	if err := RemoveUnderRoots(victim, root); err != nil {
		t.Fatalf("RemoveUnderRoots(%q, %q) = %v, want nil", victim, root, err)
	}
	if exists(victim) {
		t.Fatalf("RemoveUnder left %s behind", victim)
	}
}

// a path outside every root is refused, and left where it is.
func TestRemoveUnderRefusesOutsideTheRoots(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "keep")
	mustMkdir(t, victim)
	if err := RemoveUnderRoots(victim, root); err == nil {
		t.Fatalf("RemoveUnderRoots(%q, %q) = nil, want a refusal", victim, root)
	}
	if !exists(victim) {
		t.Fatalf("RemoveUnder removed %s, a path outside the root", victim)
	}
}

// a path containing ".." is refused even when it would resolve below the root.
func TestRemoveUnderRefusesDotDot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "slot")
	mustMkdir(t, target)
	escape := root + string(os.PathSeparator) + "slot" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "slot"
	if err := RemoveUnderRoots(escape, root); err == nil {
		t.Fatalf("RemoveUnderRoots(%q, %q) = nil, want a refusal for \"..\"", escape, root)
	}
	if !exists(target) {
		t.Fatalf("RemoveUnder removed %s through a \"..\" element", target)
	}
}

// a path that IS a symlink is refused; the target survives.
func TestRemoveUnderRefusesSymlinkPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "keep"), "x")
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := RemoveUnderRoots(link, root); err == nil {
		t.Fatalf("RemoveUnderRoots(%q, %q) = nil, want a refusal for a symlink", link, root)
	}
	if !exists(filepath.Join(outside, "keep")) {
		t.Fatalf("RemoveUnder followed the symlink and removed its target")
	}
}

// a symlink on an intermediate component that points outside the root is
// refused too: the resolved path is not below the root.
func TestResolvedUnderRefusesSymlinkEscape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "keep"), "x")
	if err := os.Symlink(outside, filepath.Join(root, "gate")); err != nil {
		t.Fatal(err)
	}
	escape := filepath.Join(root, "gate", "keep")
	if _, err := ResolvedUnder(escape, root); err == nil {
		t.Fatalf("ResolvedUnder(%q, %q) = nil, want a refusal for a symlink escape", escape, root)
	}
	if !exists(filepath.Join(outside, "keep")) {
		t.Fatalf("ResolvedUnder accepted a path that resolves outside the root")
	}
}

// createReadOnlyTree builds a tree under victim with various read-only files,
// read-only subdirectories (0o555, 0o500), and restrictive directories (0o400, 0o000).
func createReadOnlyTree(t *testing.T, victim string) {
	t.Helper()

	cDir := filepath.Join(victim, "sub", "inner", "unreadable")
	mustMkdir(t, cDir)
	mustWrite(t, filepath.Join(cDir, "deep.txt"), "deep-content")
	mustWrite(t, filepath.Join(victim, "sub", "inner", "inner.txt"), "inner-content")
	mustWrite(t, filepath.Join(victim, "sub", "ro_sub.txt"), "sub-content")
	mustWrite(t, filepath.Join(victim, "ro_file.txt"), "root-content")

	// Ensure cleanup restores permissions so t.TempDir removal never fails if a check fails.
	t.Cleanup(func() {
		_ = filepath.WalkDir(victim, func(p string, d os.DirEntry, err error) error {
			_ = os.Chmod(p, 0o700)
			return nil
		})
	})

	// Apply restrictive permissions from leaves to root.
	if err := os.Chmod(filepath.Join(cDir, "deep.txt"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cDir, 0o000); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(victim, "sub", "inner", "inner.txt"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(victim, "sub", "inner"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(victim, "sub", "ro_sub.txt"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(victim, "sub"), 0o500); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(victim, "ro_file.txt"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(victim, 0o555); err != nil {
		t.Fatal(err)
	}
}

// RemoveUnder cleanly removes a directory tree containing read-only and restrictive files/dirs.
func TestRemoveUnderRemovesReadOnlyTree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	victim := filepath.Join(root, "victim")
	createReadOnlyTree(t, victim)

	if err := RemoveUnder(root, victim); err != nil {
		t.Fatalf("RemoveUnder(%q, %q) = %v, want nil", root, victim, err)
	}
	if exists(victim) {
		t.Fatalf("RemoveUnder left %s behind", victim)
	}
	if !exists(root) {
		t.Fatalf("RemoveUnder removed root %s", root)
	}
}

// RemoveUnderRoots cleanly removes a directory tree containing read-only and restrictive files/dirs.
func TestRemoveUnderRootsRemovesReadOnlyTree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	victim := filepath.Join(root, "victim")
	createReadOnlyTree(t, victim)

	if err := RemoveUnderRoots(victim, root); err != nil {
		t.Fatalf("RemoveUnderRoots(%q, %q) = %v, want nil", victim, root, err)
	}
	if exists(victim) {
		t.Fatalf("RemoveUnderRoots left %s behind", victim)
	}
	if !exists(root) {
		t.Fatalf("RemoveUnderRoots removed root %s", root)
	}
}

// RemoveUnder removes a single read-only file directly under the root.
func TestRemoveUnderRemovesSingleReadOnlyFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	victim := filepath.Join(root, "ro_file.txt")
	mustWrite(t, victim, "ro")
	if err := os.Chmod(victim, 0o400); err != nil {
		t.Fatal(err)
	}

	if err := RemoveUnder(root, victim); err != nil {
		t.Fatalf("RemoveUnder(%q, %q) = %v, want nil", root, victim, err)
	}
	if exists(victim) {
		t.Fatalf("RemoveUnder left %s behind", victim)
	}
	if !exists(root) {
		t.Fatalf("RemoveUnder removed root %s", root)
	}
}

// RemoveUnderRoots removes a single read-only file directly under the root.
func TestRemoveUnderRootsRemovesSingleReadOnlyFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	victim := filepath.Join(root, "ro_file.txt")
	mustWrite(t, victim, "ro")
	if err := os.Chmod(victim, 0o400); err != nil {
		t.Fatal(err)
	}

	if err := RemoveUnderRoots(victim, root); err != nil {
		t.Fatalf("RemoveUnderRoots(%q, %q) = %v, want nil", victim, root, err)
	}
	if exists(victim) {
		t.Fatalf("RemoveUnderRoots left %s behind", victim)
	}
	if !exists(root) {
		t.Fatalf("RemoveUnderRoots removed root %s", root)
	}
}

// addUserWrite does not alter permissions of symlink targets outside the root.
func TestRemoveUnderPreservesSymlinkTargetPermissions(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "outside_ro.txt")
	mustWrite(t, outsideFile, "outside content")
	if err := os.Chmod(outsideFile, 0o400); err != nil {
		t.Fatal(err)
	}

	victim := filepath.Join(root, "victim")
	mustMkdir(t, victim)
	insideFile := filepath.Join(victim, "inside_ro.txt")
	mustWrite(t, insideFile, "inside content")
	if err := os.Chmod(insideFile, 0o400); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(victim, "outside_link.txt")
	if err := os.Symlink(outsideFile, symlink); err != nil {
		t.Fatal(err)
	}

	if err := RemoveUnder(root, victim); err != nil {
		t.Fatalf("RemoveUnder(%q, %q) = %v, want nil", root, victim, err)
	}
	if exists(victim) {
		t.Fatalf("RemoveUnder left %s behind", victim)
	}
	if !exists(outsideFile) {
		t.Fatalf("RemoveUnder removed symlink target %s", outsideFile)
	}
	info, err := os.Stat(outsideFile)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o400 {
		t.Fatalf("outside file perm = %#o, want 0o400", perm)
	}
}
