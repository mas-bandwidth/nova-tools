package main

// issue2255_test.go covers the licence rule-table edges from SPEC-DECIDE.md reading 6
// that had no command-level test: once the table classes a red as rerunnable, a decider's
// answer that withdraws the licence must turn rerun=licensed into rerun=no finding=yes, and
// the withdrawal must never grant a licence.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
)

// TestIssue2255 reproduces nova-tools#2255: the withdrawn decider answer is an edge of the
// licence rule table that reaches the command. Without --withdrawn the command had no way to
// say a decider took the licence back.
func TestIssue2255(t *testing.T) {
	dir := t.TempDir()
	infra := filepath.Join(dir, "infra.txt")
	if err := os.WriteFile(infra, []byte("Set up job\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		forge *stubForge
		args  []string
		want  string
	}{
		{
			name: "an infra class with no spent reruns is licensed",
			forge: &stubForge{
				jobs: []ci.FailedJob{{
					ID: 5, Name: "test (1/1)", Conclusion: "failure", Attempt: 1, HeadSHA: decideSHA,
					Steps: []ci.FailedStep{{Name: "Set up job", Conclusion: "failure"}},
				}},
				logs: map[int64]string{5: "no test event here\n"},
			},
			args: []string{"--decide", "--infra-steps", infra},
			want: "red=infra rerun=licensed finding=no reruns=0 attempt=1",
		},
		{
			name: "a decider's own-build-break or named-test answer withdraws the licence",
			forge: &stubForge{
				jobs: []ci.FailedJob{{
					ID: 5, Name: "test (1/1)", Conclusion: "failure", Attempt: 1, HeadSHA: decideSHA,
					Steps: []ci.FailedStep{{Name: "Set up job", Conclusion: "failure"}},
				}},
				logs: map[int64]string{5: "no test event here\n"},
			},
			args: []string{"--decide", "--infra-steps", infra, "--withdrawn"},
			want: "red=infra rerun=no finding=yes reruns=0 attempt=1",
		},
		{
			name: "a withdrawn licence is never granted for a cancelled leg",
			forge: &stubForge{
				jobs: []ci.FailedJob{{ID: 3, Name: "e2e", Conclusion: "cancelled", Attempt: 1, HeadSHA: decideSHA}},
				logs: map[int64]string{3: "cleanup\n"},
			},
			args: []string{"--decide", "--withdrawn"},
			want: "red=cancelled-leg rerun=no finding=yes reruns=0 attempt=1",
		},
		{
			name: "a withdrawn licence is never granted for a known flake",
			forge: &stubForge{
				jobs: []ci.FailedJob{{ID: 4, Name: "test (1/1)", Conclusion: "failure", Attempt: 1, HeadSHA: decideSHA}},
				logs: map[int64]string{4: "--- FAIL: TestFlaky (0.01s)\n"},
			},
			args: []string{"--decide", "--flakes", makeFlakes(t, dir, "TestFlaky\t#1\t2099-01-01\n"), "--now", "2026-09-19", "--withdrawn"},
			want: "red=known-flake rerun=no finding=yes reruns=0 attempt=1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{"--repo", "owner/name", "--run", "1"}, c.args...)
			code, stdout, stderr := runFailed(t, c.forge, args...)
			if code != 1 {
				t.Fatalf("exit = %d, want 1; stderr: %s", code, stderr)
			}
			if !strings.Contains(lastLine(stdout), c.want) {
				t.Errorf("closing line = %q, want it to carry %q", lastLine(stdout), c.want)
			}
		})
	}
}

// makeFlakes writes one flake table to a file inside t.TempDir and returns its path.
func makeFlakes(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "flakes.tsv")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
