package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func initCLITestGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v\n%s", err, string(out))
	}
	for _, args := range [][]string{
		{"config", "user.name", "Rowan"},
		{"config", "user.email", "rowan@mas-bandwidth.com"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}
	initFile := filepath.Join(dir, "init.txt")
	if err := os.WriteFile(initFile, []byte("initial commit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "init.txt"},
		{"commit", "-m", "initial commit"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}
}

// Table-driven test exercising the --commit entrypoint across CLI forms.
func TestHarvestCLICommitEntrypointTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantExit   int
		wantErrSub string
	}{
		{
			name:       "harvest --working without commit flag parses correctly",
			args:       []string{"harvest", "--working", "/nonexistent-path-abc"},
			wantExit:   2,
			wantErrSub: "HARVEST REFUSED working=/nonexistent-path-abc",
		},
		{
			name:       "harvest --working with --commit flag parses correctly",
			args:       []string{"harvest", "--working", "/nonexistent-path-abc", "--commit"},
			wantExit:   2,
			wantErrSub: "HARVEST REFUSED working=/nonexistent-path-abc",
		},
		{
			name:       "harvest --working with --commit=true parses correctly",
			args:       []string{"harvest", "--working", "/nonexistent-path-abc", "--commit=true"},
			wantExit:   2,
			wantErrSub: "HARVEST REFUSED working=/nonexistent-path-abc",
		},
		{
			name:       "harvest --working with --commit=false parses correctly",
			args:       []string{"harvest", "--working", "/nonexistent-path-abc", "--commit=false"},
			wantExit:   2,
			wantErrSub: "HARVEST REFUSED working=/nonexistent-path-abc",
		},
		{
			name:       "harvest --bench with invalid boolean for --commit fails flag parsing",
			args:       []string{"harvest", "--bench", "hulk", "--root", "/r", "--clone", "/c", "--commit=notabool"},
			wantExit:   2,
			wantErrSub: "invalid boolean value",
		},
		{
			name:       "harvest --bench defaults commit to true and proceeds to clone check",
			args:       []string{"harvest", "--bench", "hulk", "--root", "/home/gaffer/rowan-swarm-root"},
			wantExit:   2,
			wantErrSub: "--clone is required with --bench",
		},
		{
			name:       "harvest --bench with explicit --commit=false proceeds to clone check",
			args:       []string{"harvest", "--bench", "hulk", "--root", "/home/gaffer/rowan-swarm-root", "--commit=false"},
			wantExit:   2,
			wantErrSub: "--clone is required with --bench",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out, errb bytes.Buffer
			code := run(tc.args, &out, &errb, time.Now().UTC())
			if code != tc.wantExit {
				t.Fatalf("exit code = %d, want %d\nstdout=%s\nstderr=%s", code, tc.wantExit, out.String(), errb.String())
			}
			if tc.wantErrSub != "" && !strings.Contains(errb.String(), tc.wantErrSub) {
				t.Errorf("stderr %q does not contain expected substring %q", errb.String(), tc.wantErrSub)
			}
		})
	}
}

// Test that CLI `nova-pulse harvest --working <dir> --commit` actually invokes CommitJob and commits uncommitted work.
func TestHarvestCLIWorkingCommitInvoked(t *testing.T) {
	t.Parallel()

	working := t.TempDir()
	jobDir := filepath.Join(working, "tmp", "guid-1", "jobs", "card-cli")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	resContent := "RESULT card-cli sha=012345678901\nDONE\nBRANCH rowan/card-cli\nREPO o/r\n"
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(resContent), 0o644); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(jobDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initCLITestGitRepo(t, repoDir)

	// Add an uncommitted work file
	workFile := filepath.Join(repoDir, "work.txt")
	if err := os.WriteFile(workFile, []byte("uncommitted CLI work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	// Run via the CLI run() entrypoint with --commit
	code := run([]string{
		"harvest",
		"--working", working,
		"--commit",
		"--clone", "o/r=" + t.TempDir(),
	}, &out, &errb, time.Unix(0, 0).UTC())

	// Verify CommitJob ran and committed the work
	cmd := exec.Command("git", "log", "-1", "--format=%s")
	cmd.Dir = repoDir
	logOut, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}
	if !strings.Contains(string(logOut), "RESULT card-cli") {
		t.Fatalf("expected uncommitted work to be committed by --commit, got log: %s", string(logOut))
	}
	if !strings.Contains(out.String(), "COMMITTED card-cli rowan/card-cli") {
		t.Errorf("stdout does not contain COMMITTED verdict:\n%s", out.String())
	}
	_ = code
}

// Negative control: without --commit, uncommitted work remains uncommitted.
func TestHarvestCLIWorkingWithoutCommitLeavesUncommitted(t *testing.T) {
	t.Parallel()

	working := t.TempDir()
	jobDir := filepath.Join(working, "tmp", "guid-1", "jobs", "card-no-commit")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	resContent := "RESULT card-no-commit sha=012345678901\nDONE\nBRANCH rowan/card-no-commit\nREPO o/r\n"
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(resContent), 0o644); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(jobDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initCLITestGitRepo(t, repoDir)

	workFile := filepath.Join(repoDir, "work.txt")
	if err := os.WriteFile(workFile, []byte("uncommitted work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	_ = run([]string{
		"harvest",
		"--working", working,
		"--clone", "o/r=" + t.TempDir(),
	}, &out, &errb, time.Unix(0, 0).UTC())

	// Verify CommitJob did not run: git log does not have "RESULT card-no-commit"
	cmd := exec.Command("git", "log", "-1", "--format=%s")
	cmd.Dir = repoDir
	logOut, _ := cmd.CombinedOutput()
	if strings.Contains(string(logOut), "RESULT card-no-commit") {
		t.Fatalf("work was unexpectedly committed when --commit was omitted: %s", string(logOut))
	}
	if strings.Contains(out.String(), "COMMITTED card-no-commit") {
		t.Errorf("stdout unexpectedly contains COMMITTED verdict:\n%s", out.String())
	}
}
