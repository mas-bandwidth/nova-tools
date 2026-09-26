package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLessonAppendVerb(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "LESSONS.md"), []byte("# Lessons\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"lesson", "append", "--repo", repo, "--ids", "s9-001", "--component", "brief", "--kind", "read", "--failure", "missing context", "--prevention", "read lessons", "--evidence", "nova-tools#2498", "--status", "active", "--reviewed-by", "stella"}
	code, stdout, stderr := runSprint(args...)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "LESSON APPENDED id=s9-001") {
		t.Fatalf("append: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, stdout, stderr = runSprint(args...)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "LESSON UNCHANGED id=s9-001") {
		t.Fatalf("retry: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, stdout, stderr = runSprint("lesson", "supersede", "--repo", repo, "--ids", "s9-001")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "LESSON SUPERSEDED id=s9-001") || !strings.Contains(stdout, "LESSONS-ARCHIVE.md") {
		t.Fatalf("supersede: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, stdout, stderr = runSprint("lesson", "supersede", "--repo", repo, "--ids", "s9-001")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "LESSON UNCHANGED id=s9-001") {
		t.Fatalf("supersede retry: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}
