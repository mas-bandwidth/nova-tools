package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldWritesConfinedFiles(t *testing.T) {
	t.Parallel()

	tree := t.TempDir()
	outs := []Planned{
		{Rel: "pkg/a.txt", Data: []byte("hello a")},
		{Rel: "pkg/sub/b.txt", Data: []byte("hello b")},
	}

	written, err := Write(tree, outs)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(written) != 2 {
		t.Fatalf("Write returned %d paths, want 2", len(written))
	}

	for _, o := range outs {
		data, err := os.ReadFile(filepath.Join(tree, filepath.FromSlash(o.Rel)))
		if err != nil {
			t.Errorf("read %s: %v", o.Rel, err)
		}
		if string(data) != string(o.Data) {
			t.Errorf("%s = %q, want %q", o.Rel, data, o.Data)
		}
	}

	// Overwrite refused
	_, err = Write(tree, outs)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected already exists error, got %v", err)
	}
}

func TestScaffoldRefusesSymlink(t *testing.T) {
	t.Parallel()

	tree := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "secret.txt")
	_ = os.WriteFile(target, []byte("secret"), 0o644)

	link := filepath.Join(tree, "evil_link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	outs := []Planned{
		{Rel: "evil_link", Data: []byte("overwrite")},
	}
	_, err := Write(tree, outs)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink refusal, got %v", err)
	}

	// Verify outside file was untouched
	data, _ := os.ReadFile(target)
	if string(data) != "secret" {
		t.Errorf("outside file was modified: %s", data)
	}
}

func TestScaffoldRefusesADanglingFinalSymlink(t *testing.T) {
	t.Parallel()

	tree := t.TempDir()
	link := filepath.Join(tree, "dangling")
	if err := os.Symlink("nonexistent", link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	outs := []Planned{
		{Rel: "dangling", Data: []byte("hello")},
	}
	_, err := Write(tree, outs)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink refusal for dangling symlink, got %v", err)
	}
}

func TestScaffoldRefusesASymlinkedParentDirectory(t *testing.T) {
	t.Parallel()

	tree := t.TempDir()
	realDir := t.TempDir()
	link := filepath.Join(tree, "linked_dir")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	outs := []Planned{
		{Rel: "linked_dir/file.txt", Data: []byte("hello")},
	}
	_, err := Write(tree, outs)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink refusal for symlinked parent dir, got %v", err)
	}
}

func TestScaffoldNeverOverwritesACollisionRacedInAfterThePreflight(t *testing.T) {
	tree := t.TempDir()
	outs := []Planned{
		{Rel: "pkg/raced.txt", Data: []byte("new content")},
	}
	BeforeCreate = func(rel string) {
		p := filepath.Join(tree, filepath.FromSlash(rel))
		_ = os.WriteFile(p, []byte("pre-existing content"), 0o644)
	}
	defer func() { BeforeCreate = nil }()

	_, err := Write(tree, outs)
	if err == nil || !strings.Contains(err.Error(), "appeared while scaffold was writing") {
		t.Fatalf("expected raced collision refusal, got %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(tree, "pkg", "raced.txt"))
	if string(data) != "pre-existing content" {
		t.Fatalf("file overwritten despite race: %s", data)
	}
}

func TestScaffoldRuleAndVerbBasic(t *testing.T) {
	t.Parallel()

	tree := t.TempDir()
	_ = os.WriteFile(filepath.Join(tree, "go.mod"), []byte("module test"), 0o644)

	// Rule
	writtenRule, err := Rule(tree, "sample")
	if err != nil {
		t.Fatalf("Rule failed: %v", err)
	}
	if len(writtenRule) != 3 {
		t.Fatalf("Rule wrote %d files, want 3", len(writtenRule))
	}

	// Verb
	writtenVerb, err := Verb(tree, "nova-ci", "sample")
	if err != nil {
		t.Fatalf("Verb failed: %v", err)
	}
	if len(writtenVerb) != 4 {
		t.Fatalf("Verb wrote %d files, want 4", len(writtenVerb))
	}
}
