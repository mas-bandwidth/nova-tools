package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue2331(t *testing.T) {
	t.Parallel()

	t.Run("session-start-refuses-held-journal", func(t *testing.T) {
		dir := t.TempDir()
		journal := filepath.Join(dir, "test.journal")
		lockPath := journal + ".lock"
		if err := os.WriteFile(lockPath, []byte("pid=4242 socket=other.sock\n"), 0644); err != nil {
			t.Fatal(err)
		}

		socketPath := filepath.Join(dir, "test.sock")
		var stdout, stderr bytes.Buffer
		code := run([]string{"session", "start",
			"--session", socketPath,
			"--as", "rowan",
			"--file", "snapshot.sexp",
			"--journal", journal,
			"--repo", "mas-bandwidth/nova-tools",
			"--remote", "origin",
			"--branch", "dev",
			"--git-timeout", "30",
		}, &stdout, &stderr, "")

		if code != 1 {
			t.Fatalf("session start with held journal exit = %d, want 1 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("journal-held refusal wrote stdout: %q", stdout.String())
		}
		stderrStr := stderr.String()
		if !strings.Contains(stderrStr, "journal held") {
			t.Fatalf("journal-held refusal does not name 'journal held': %q", stderrStr)
		}
		if !strings.Contains(stderrStr, "pid 4242") {
			t.Fatalf("journal-held refusal does not name pid: %q", stderrStr)
		}
	})

	t.Run("session-start-refuses-held-socket", func(t *testing.T) {
		dir := t.TempDir()
		socketPath := filepath.Join(dir, "held.sock")
		lockPath := socketPath + ".lock"
		if err := os.WriteFile(lockPath, []byte("pid=5353 journal=/tmp/other.journal\n"), 0644); err != nil {
			t.Fatal(err)
		}

		var stdout, stderr bytes.Buffer
		code := run([]string{"session", "start",
			"--session", socketPath,
			"--as", "rowan",
			"--file", "snapshot.sexp",
			"--journal", filepath.Join(dir, "some.journal"),
			"--repo", "mas-bandwidth/nova-tools",
			"--remote", "origin",
			"--branch", "dev",
			"--git-timeout", "30",
		}, &stdout, &stderr, "")

		if code != 1 {
			t.Fatalf("session start with held socket exit = %d, want 1 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("socket-held refusal wrote stdout: %q", stdout.String())
		}
		stderrStr := stderr.String()
		if !strings.Contains(stderrStr, "socket held") {
			t.Fatalf("socket-held refusal does not name 'socket held': %q", stderrStr)
		}
	})

	t.Run("resume-predicate-all-cases", func(t *testing.T) {
		cases := []struct {
			name string
			line string
			want string
		}{
			{
				name: "names-nobody",
				line: "SESSION FAIL engine=rowan generation=2: taken by another owner",
				want: "taken",
			},
			{
				name: "skew-past",
				line: "SESSION FAIL owner=other generation=2 until=2026-01-01T00:00:00Z held",
				want: "held",
			},
			{
				name: "bench-mismatch",
				line: "SESSION FAIL owner=rowan generation=3 bench=other-bench: copied journal, not resumed",
				want: "copied journal",
			},
			{
				name: "token-mismatch",
				line: "SESSION FAIL owner=rowan generation=3: journal lock not held",
				want: "journal lock not held",
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				code := printReply(tc.line, &stdout, &stderr)
				if code != 1 {
					t.Fatalf("resume predicate %s exit = %d, want 1", tc.name, code)
				}
				if stdout.Len() != 0 {
					t.Fatalf("resume predicate %s wrote stdout: %q", tc.name, stdout.String())
				}
				if !strings.Contains(stderr.String(), tc.want) {
					t.Fatalf("resume predicate %s stderr %q does not name %q", tc.name, stderr.String(), tc.want)
				}
			})
		}
	})

	t.Run("handoff-fences-writes", func(t *testing.T) {
		fencedLine := "SESSION FAIL fenced generation=3 until=2026-09-16T18:00:00Z"
		var stdout, stderr bytes.Buffer
		code := printReply(fencedLine, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("fenced write exit = %d, want 1", code)
		}
		if stdout.Len() != 0 {
			t.Fatalf("fenced write wrote stdout: %q", stdout.String())
		}
		if !strings.Contains(stderr.String(), "fenced") {
			t.Fatalf("fenced refusal does not name 'fenced': %q", stderr.String())
		}
	})
}
