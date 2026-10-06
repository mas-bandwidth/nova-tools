package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportUnreadableWithLines(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tr := mkdir(t, filepath.Join(dir, "tr"))
	repos := reposFile(t, dir)

	// Create a readable transcript file that produces body lines (using helper msg)
	readablePath := filepath.Join(tr, "readable.jsonl")
	write(t, readablePath, msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100, "output_tokens": 10}, "/x/schema/a.go")+"\n")

	// Create an unreadable transcript file (using helper msg)
	unreadablePath := filepath.Join(tr, "unreadable.jsonl")
	write(t, unreadablePath, msg("m2", "2026-09-11T11:00:00Z", "fable", map[string]int{"input_tokens": 200, "output_tokens": 20}, "/x/schema/b.go")+"\n")
	if err := os.Chmod(unreadablePath, 0o000); err != nil {
		t.Skip("cannot remove read permission (running as root?):", err)
	}

	r := invoke(t, "report", "--who", "test", "--day", "2026-09-11", "--repos", repos, "--claude", "glenn="+tr)

	t.Logf("exit: %d", r.exit)
	t.Logf("stdout:\n%s", r.stdout)
	t.Logf("stderr:\n%s", r.stderr)

	// Verify the exit code is 1 (because of unreadable file)
	if r.exit != 1 {
		t.Errorf("expected exit 1, got %d", r.exit)
	}

	// Should NOT say REPORT OK when there are unreadable files
	if strings.Contains(r.stderr, "REPORT OK") {
		t.Errorf("output should not contain REPORT OK when there are unreadable files")
	}

	// Should say REPORT FAIL with the unreadable count
	if !strings.Contains(r.stderr, "REPORT FAIL") {
		t.Errorf("output should contain REPORT FAIL")
	}

	// Should have body lines from the readable file
	if !strings.Contains(r.stdout, "fable") {
		t.Errorf("output should have body lines from readable files: stdout=%s", r.stdout)
	}

	// Restore permission for cleanup
	os.Chmod(unreadablePath, 0o644)
}
