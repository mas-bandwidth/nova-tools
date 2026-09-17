package safepath

import (
	"os"
	"path/filepath"
	"testing"
)

// safepath's contract, in the order the danger arrives: an empty path, the root itself, a
// path that resolves outside the root, a path that IS a symlink, and a root that is the
// whole disk or the user's home. Every one of them is a refusal, and every refusal leaves
// the bytes where they were. Glenn, 2026-09-17: "It is just one mistake away from deleting
// the whole disk."
func TestRemoveUnderRefusesUnsafePaths(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	// A path that only LOOKS like it is under the root: the symlink in the middle of it
	// resolves to another tree, so EvalSymlinks on both must catch it.
	escaped := filepath.Join(link, "escaped")
	if err := os.MkdirAll(escaped, 0o755); err != nil {
		t.Fatal(err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("this machine has no home directory to test the refusal against: %v", err)
	}

	cases := []struct {
		name string
		root string
		path string
	}{
		{"empty path", root, ""},
		{"empty root", "", inside},
		{"path is the root", root, root},
		{"path is not below the root", root, outside},
		{"path escapes the root through a symlink", root, escaped},
		{"path is itself a symlink", root, link},
		{"root is the whole disk", "/", filepath.Join(root, "inside")},
		{"root is the user's home", home, filepath.Join(home, "somewhere")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := RemoveUnder(c.root, c.path); err == nil {
				t.Fatalf("RemoveUnder(%q, %q) returned nil; it must refuse", c.root, c.path)
			}
			// Nothing under the temp root may have moved, and the symlink target must
			// still be there: a refusal is not a partial removal.
			for _, p := range []string{inside, link, outside} {
				if _, err := os.Lstat(p); err != nil {
					t.Errorf("the refusal removed %s: %v", p, err)
				}
			}
		})
	}
}

// The one path it removes: a plain directory strictly below a root the caller named.
func TestRemoveUnderRemovesBelowRoot(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(filepath.Join(sub, "deep", "deeper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveUnder(root, sub); err != nil {
		t.Fatalf("RemoveUnder removed nothing: %v", err)
	}
	if _, err := os.Lstat(sub); !os.IsNotExist(err) {
		t.Fatalf("the directory below the root is still there: %v", err)
	}
	if _, err := os.Lstat(root); err != nil {
		t.Fatalf("the root itself was removed: %v", err)
	}
}
