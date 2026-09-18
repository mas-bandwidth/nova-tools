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

// remove-under-is-the-only-rm: a directory strictly below a root goes. This is
// the install verb's own case, named apart from dev's RemoveUnder test.
func TestInstallRemoveUnderRemovesBelowRoot(t *testing.T) {
	root := t.TempDir()
	victim := filepath.Join(root, "slot", "jobs", "card-1")
	mustWrite(t, filepath.Join(victim, "scratch", "s"), "x")
	if err := RemoveUnder(root, victim); err != nil {
		t.Fatalf("RemoveUnder(%q, %q) = %v, want nil", root, victim, err)
	}
	if exists(victim) {
		t.Fatalf("RemoveUnder left %s behind", victim)
	}
}

// a path outside every root is refused, and left where it is.
func TestRemoveUnderRefusesOutsideTheRoots(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "keep")
	mustMkdir(t, victim)
	if err := RemoveUnder(root, victim); err == nil {
		t.Fatalf("RemoveUnder(%q, %q) = nil, want a refusal", root, victim)
	}
	if !exists(victim) {
		t.Fatalf("RemoveUnder removed %s, a path outside the root", victim)
	}
}

// a path containing ".." is refused even when it would resolve below the root.
func TestRemoveUnderRefusesDotDot(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "slot")
	mustMkdir(t, target)
	escape := root + string(os.PathSeparator) + "slot" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "slot"
	if err := RemoveUnder(root, escape); err == nil {
		t.Fatalf("RemoveUnder(%q, %q) = nil, want a refusal for \"..\"", root, escape)
	}
	if !exists(target) {
		t.Fatalf("RemoveUnder removed %s through a \"..\" element", target)
	}
}

// a path that IS a symlink is refused; the target survives.
func TestRemoveUnderRefusesSymlinkPath(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "keep"), "x")
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := RemoveUnder(root, link); err == nil {
		t.Fatalf("RemoveUnder(%q, %q) = nil, want a refusal for a symlink", root, link)
	}
	if !exists(filepath.Join(outside, "keep")) {
		t.Fatalf("RemoveUnder followed the symlink and removed its target")
	}
}

// a symlink on an intermediate component that points outside the root is
// refused too: the resolved path is not below the root.
func TestResolvedUnderRefusesSymlinkEscape(t *testing.T) {
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
