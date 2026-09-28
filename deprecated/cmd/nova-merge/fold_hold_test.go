package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// The keep-both write never follows a symlink: a conflict path that is a link, or whose
// parent directory is a link, to somewhere outside the scratch clone is refused and the
// outside file is untouched.
func TestFoldWriteUnderRefusesSymlinks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "target_test.go")
	if err := os.WriteFile(target, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "x_test.go")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "sub")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"x_test.go", "sub/target_test.go"} {
		if err := foldWriteUnder(root, rel, []byte("written\n")); err == nil {
			t.Fatalf("foldWriteUnder wrote through the symlink at %s", rel)
		}
	}
	if b, _ := os.ReadFile(target); string(b) != "outside\n" {
		t.Fatalf("the file outside the scratch clone was changed: %q", b)
	}
	plain := filepath.Join(root, "plain_test.go")
	if err := testbin.WriteExecutable(plain, []byte("old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := foldWriteUnder(root, "plain_test.go", []byte("new\n")); err != nil {
		t.Fatalf("a plain file under the root was refused: %v", err)
	}
	info, _ := os.Stat(plain)
	if b, _ := os.ReadFile(plain); string(b) != "new\n" || info.Mode().Perm() != 0o755 {
		t.Fatalf("the plain file was not rewritten in place with its mode: %q %v", b, info.Mode())
	}
}

// A bare numeric card is a card, never a pull request number; only `#<n>` is.
func TestFoldBodySupersedesOnlyPullRequestReferences(t *testing.T) {
	t.Parallel()
	body := foldBody([]foldBranch{{Name: "feature-a", Cards: []string{"101", "#77", "card-9"}}}, "main", "fold-out")
	absent(t, body, "supersedes #101")
	contains(t, body, "supersedes #77 branch=feature-a")
	got := supersededPRs(body)
	if len(got) != 1 || got[0].PR != 77 || got[0].Branch != "feature-a" {
		t.Fatalf("supersededPRs read %v, want [{77 feature-a}]", got)
	}
}

// A mixed tree runs the nova-work script AND go test over the changed Go packages.
func TestFoldTreeCommandsRunGoForChangedPackagesInAMixedTree(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "lisp", "nova-work", "run-tests.sh"), "#!/bin/sh\n")
	mustWriteFile(t, filepath.Join(dir, "pkg", "a.go"), "package pkg\n")
	mustWriteFile(t, filepath.Join(dir, "a.go"), "package root\n")

	changed := []string{"pkg/a.go", "README.md", "gone/b.go", "pkg/a.go", "a.go"}
	// No go.mod: not a Go module, the script alone.
	if got := foldTreeCommands(dir, changed); !reflect.DeepEqual(got, [][]string{{"bash", "lisp/nova-work/run-tests.sh"}}) {
		t.Fatalf("a nova-work tree with no go.mod: %v", got)
	}
	mustWriteFile(t, filepath.Join(dir, "go.mod"), "module x\n")
	want := [][]string{{"bash", "lisp/nova-work/run-tests.sh"}, {"go", "test", "./pkg", "."}}
	if got := foldTreeCommands(dir, changed); !reflect.DeepEqual(got, want) {
		t.Fatalf("a mixed tree: got %v, want %v", got, want)
	}
	if got := foldTreeCommands(dir, []string{"lisp/x.lisp"}); !reflect.DeepEqual(got, want[:1]) {
		t.Fatalf("a mixed tree with no Go change: %v", got)
	}
	goOnly := t.TempDir()
	if got := foldTreeCommands(goOnly, nil); !reflect.DeepEqual(got, [][]string{{"go", "test", "./..."}}) {
		t.Fatalf("a Go tree: %v", got)
	}
}

func mustWriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
