package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A computed path that only LOOKS under the root must be refused before it is removed:
// a symlink inside the root that points at a directory outside it resolves outside the
// root, so removeUnder refuses and the outside directory survives. This is the red case
// the class rule of 2026-09-17 exists for -- "It shouldn't be able to delete arbitrary
// directories" -- because a lexical check alone follows the link and deletes the target.
func TestRemoveUnderRefusesATargetThatEscapesThroughASymlink(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	escaped := filepath.Join(outside, "escaped")
	if err := os.MkdirAll(filepath.Join(escaped, "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(link, "escaped")

	if err := removeUnder(root, target); err == nil {
		t.Fatal("removeUnder removed a computed path that resolves outside its root; it must refuse")
	}
	if _, err := os.Stat(filepath.Join(escaped, "deep")); err != nil {
		t.Fatalf("the outside directory did not survive the refusal: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("the outside root did not survive the refusal: %v", err)
	}
}
