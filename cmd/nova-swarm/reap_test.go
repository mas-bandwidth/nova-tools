package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// issue #1048: the reap verb frees a finished slot's data/, tmp/ and jobs/*/scratch, keeps
// the evidence, and reports the bytes freed on one line.
func TestReapVerbFreesAFinishedSlot(t *testing.T) {
	root := t.TempDir()
	slot := filepath.Join(root, "1")
	job := filepath.Join(slot, "jobs", "card-a")
	for _, p := range []struct {
		path string
		size int
	}{
		{filepath.Join(slot, "data", "go-build.bin"), 4096},
		{filepath.Join(slot, "tmp", "card-a", "scratch.tmp"), 1024},
		{filepath.Join(job, "scratch", "work"), 2048},
		{filepath.Join(job, "RESULT.md"), 128},
		{filepath.Join(job, "usage.tsv"), 64},
		{filepath.Join(job, "harness.log"), 32},
	} {
		if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p.path, make([]byte, p.size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	rc := run([]string{"reap", "--root", root}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("reap rc=%d, stderr=%s", rc, stderr.String())
	}
	if !strings.Contains(stdout.String(), "REAP OK slots=1 freed=") {
		t.Errorf("reap printed %q, want REAP OK slots=1 freed=<n>", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(slot, "data")); !os.IsNotExist(err) {
		t.Errorf("data/ was not removed")
	}
	if _, err := os.Stat(filepath.Join(job, "RESULT.md")); err != nil {
		t.Errorf("RESULT.md was removed: %v", err)
	}
	// --dry-run counts without removing.
	var dryOut, dryErr bytes.Buffer
	rc = run([]string{"reap", "--root", root, "--dry-run"}, strings.NewReader(""), &dryOut, &dryErr, time.Now())
	if rc != 0 {
		t.Fatalf("dry-run reap rc=%d, stderr=%s", rc, dryErr.String())
	}
	if _, err := os.Stat(filepath.Join(job, "RESULT.md")); err != nil {
		t.Errorf("dry-run removed RESULT.md: %v", err)
	}
}
