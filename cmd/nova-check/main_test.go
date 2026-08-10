package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The no-guessing rule at the CLI: a missing flag is a refusal (exit 2)
// that names the flag, never a fallback to a default path.
func TestRunRefusesToGuess(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{"no subcommand", nil, "usage"},
		{"unknown subcommand", []string{"frobnicate"}, "unknown subcommand"},
		{"attest without home", []string{"attest", "--manifest", "m.txt"}, "--home is required"},
		{"attest without manifest", []string{"attest", "--home", "."}, "--manifest is required"},
		{"links without dir", []string{"links"}, "--dir is required"},
		{"kernel without file", []string{"kernel", "--max-bytes", "1000"}, "--file is required"},
		{"kernel without budget", []string{"kernel", "--file", "k.md"}, "--max-bytes"},
		{"kernel with zero budget", []string{"kernel", "--file", "k.md", "--max-bytes", "0"}, "positive"},
		{"nocode without dir", []string{"nocode"}, "--dir is required"},
		{"stray positional argument", []string{"links", "--dir", ".", "extra"}, "unexpected argument"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := run(tt.args, &stdout, &stderr); got != 2 {
				t.Errorf("exit = %d, want 2; stderr: %s", got, stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

// Reviewer suggestion: with two required flags missing, the error order came
// from map iteration and differed run to run. It must be sorted, every time.
func TestRequiredFlagErrorOrderDeterministic(t *testing.T) {
	for i := 0; i < 20; i++ {
		var stdout, stderr bytes.Buffer
		if got := run([]string{"attest"}, &stdout, &stderr); got != 2 {
			t.Fatalf("exit = %d, want 2", got)
		}
		out := stderr.String()
		homeIdx := strings.Index(out, "--home is required")
		manifestIdx := strings.Index(out, "--manifest is required")
		if homeIdx < 0 || manifestIdx < 0 {
			t.Fatalf("stderr must name both missing flags, got %q", out)
		}
		if homeIdx > manifestIdx {
			t.Fatalf("flag errors out of sorted order (run %d): %q", i, out)
		}
	}
}

// End to end through run(): each subcommand passing on good input and
// failing (exit 1, FAIL on stderr) on bad input.
func TestRunEndToEnd(t *testing.T) {
	home := t.TempDir()
	mustWrite(t, home, "KERNEL.md", "the kernel\n")
	mustWrite(t, home, "pattern/p.md", "[k](../KERNEL.md)\n")
	manifest := filepath.Join(t.TempDir(), "manifest.txt")
	if err := os.WriteFile(manifest, []byte("KERNEL.md\npattern/p.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		args       []string
		wantExit   int
		wantStdout string
		wantStderr string
	}{
		{"attest passes", []string{"attest", "--home", home, "--manifest", manifest}, 0, "ATTEST OK files=2 bytes=29 sha256=", ""},
		{"links passes", []string{"links", "--dir", home}, 0, "LINKS OK files=2 links=1", ""},
		{"kernel passes", []string{"kernel", "--file", filepath.Join(home, "KERNEL.md"), "--max-bytes", "100"}, 0, "KERNEL OK bytes=11 budget=100", ""},
		{"nocode passes", []string{"nocode", "--dir", home}, 0, "NOCODE OK files=2 clean", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := run(tt.args, &stdout, &stderr); got != tt.wantExit {
				t.Fatalf("exit = %d, want %d; stderr: %s", got, tt.wantExit, stderr.String())
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
		})
	}

	// Now break the tree and watch every check say NO.
	mustWrite(t, home, "pattern/broken.md", "[gone](nowhere.md)\n")
	mustWrite(t, home, "sneaky.sh", "echo pwned\n")
	if err := os.Remove(filepath.Join(home, "KERNEL.md")); err != nil {
		t.Fatal(err)
	}

	failing := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{"attest fails on missing file", []string{"attest", "--home", home, "--manifest", manifest}, "ATTEST FAIL KERNEL.md: does not exist"},
		{"links fails on broken link", []string{"links", "--dir", home}, "LINKS FAIL pattern/broken.md:1: nowhere.md"},
		{"kernel fails on missing kernel", []string{"kernel", "--file", filepath.Join(home, "KERNEL.md"), "--max-bytes", "100"}, "KERNEL FAIL"},
		{"nocode fails on shell script", []string{"nocode", "--dir", home}, "NOCODE FAIL sneaky.sh: code extension .sh"},
	}
	for _, tt := range failing {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := run(tt.args, &stdout, &stderr); got != 1 {
				t.Fatalf("exit = %d, want 1; stdout: %s stderr: %s", got, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
			if stdout.String() != "" {
				t.Errorf("a failing check must not print an OK line, got %q", stdout.String())
			}
		})
	}
}

// The unreadable-.md seam a caller actually sees: `LINKS FAIL <file>:
// unreadable (<why>)` on stderr — a whole-file line, no line number, no
// target — beside the ordinary broken-link line, and exit 1. The code this
// was first run against exited 2 and printed NO findings at all: one
// chmod-000 file converted the run into a refusal and discarded the real
// broken link. That discard is the defect this test pins shut.
func TestLinksUnreadableFileIsNamedFailureAtTheCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows: chmod 0 does not refuse reads, so this property cannot be observed here")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not refuse, so this property cannot be observed here")
	}
	dir := t.TempDir()
	mustWrite(t, dir, "broken.md", "[gone](missing.md)\n")
	mustWrite(t, dir, "locked.md", "text\n")
	locked := filepath.Join(dir, "locked.md")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })

	var stdout, stderr bytes.Buffer
	if got := run([]string{"links", "--dir", dir}, &stdout, &stderr); got != 1 {
		t.Fatalf("exit = %d, want 1 -- named failures, not a refusal; stderr: %s", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "LINKS FAIL locked.md: unreadable") {
		t.Errorf("stderr = %q, want the whole-file grammar LINKS FAIL <file>: unreadable (<why>)", stderr.String())
	}
	if !strings.Contains(stderr.String(), "LINKS FAIL broken.md:1: missing.md (does not exist)") {
		t.Errorf("stderr = %q, want the accumulated broken link kept, not discarded", stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("a failing check must not print an OK line, got %q", stdout.String())
	}
}

func mustWrite(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
