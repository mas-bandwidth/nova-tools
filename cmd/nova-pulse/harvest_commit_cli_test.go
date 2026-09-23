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

// Table-driven or paired test asserting that policy refusals act as a caller gate in the CLI:
// stops that job, fails closed (non-zero exit), halts before dependent effects (zero push, zero PR, no .harvested marker),
// and leaves scratch/repo/index/branch state unchanged, paired with allowed success (Finding 1 & 2).
func TestHarvestCLIRefusedJobPolicyCallerGate(t *testing.T) {
	t.Parallel()

	working := t.TempDir()

	// 1. Refused Job: card declares PATHS pkg/valid.go, but modifies unauthorized/bad.go
	// Also has a scratch file notes.txt that must NOT be moved because of the refusal.
	refusedJobDir := filepath.Join(working, "tmp", "guid-1", "jobs", "card-refused")
	if err := os.MkdirAll(refusedJobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	refusedRes := "RESULT card-refused sha=012345678901\nDONE\nBRANCH rowan/card-refused\nREPO o/r\nPATHS pkg/valid.go\n"
	if err := os.WriteFile(filepath.Join(refusedJobDir, "RESULT.md"), []byte(refusedRes), 0o644); err != nil {
		t.Fatal(err)
	}
	refusedRepo := filepath.Join(refusedJobDir, "repo")
	if err := os.MkdirAll(refusedRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	initCLITestGitRepo(t, refusedRepo)
	// Place scratch file in refusedRepo
	refusedScratch := filepath.Join(refusedRepo, "notes.txt")
	if err := os.WriteFile(refusedScratch, []byte("scratch in refused repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Place unauthorized dirty file outside declared PATHS
	badFileDir := filepath.Join(refusedRepo, "unauthorized")
	if err := os.MkdirAll(badFileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	badFile := filepath.Join(badFileDir, "bad.go")
	if err := os.WriteFile(badFile, []byte("package bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. Allowed Job: card declares PATHS pkg/valid.go and modifies pkg/valid.go
	// Also has scratch file notes.txt which SHOULD be set aside into scratch-from-repo/
	allowedJobDir := filepath.Join(working, "tmp", "guid-1", "jobs", "card-allowed")
	if err := os.MkdirAll(allowedJobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	allowedRes := "RESULT card-allowed sha=012345678901\nDONE\nBRANCH rowan/card-allowed\nREPO o/r\nPATHS pkg/valid.go\n"
	if err := os.WriteFile(filepath.Join(allowedJobDir, "RESULT.md"), []byte(allowedRes), 0o644); err != nil {
		t.Fatal(err)
	}
	allowedRepo := filepath.Join(allowedJobDir, "repo")
	if err := os.MkdirAll(allowedRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	initCLITestGitRepo(t, allowedRepo)
	allowedScratch := filepath.Join(allowedRepo, "notes.txt")
	if err := os.WriteFile(allowedScratch, []byte("scratch in allowed repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	goodFileDir := filepath.Join(allowedRepo, "pkg")
	if err := os.MkdirAll(goodFileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	goodFile := filepath.Join(goodFileDir, "valid.go")
	if err := os.WriteFile(goodFile, []byte("package valid\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Coordinator clone setup
	coordClone := t.TempDir()
	initCLITestGitRepo(t, coordClone)

	var out, errb bytes.Buffer
	code := run([]string{
		"harvest",
		"--working", working,
		"--commit",
		"--clone", "o/r=" + coordClone,
	}, &out, &errb, time.Unix(0, 0).UTC())

	// Exit code must be non-zero (fails closed because card-refused failed)
	if code == 0 {
		t.Fatalf("expected non-zero exit code due to refused job, got 0\nstdout:\n%s\nstderr:\n%s", out.String(), errb.String())
	}

	// Stderr must log refusal error for card-refused
	if !strings.Contains(errb.String(), "HARVEST COMMIT ERROR") || !strings.Contains(errb.String(), "PATHS REFUSED") {
		t.Errorf("expected HARVEST COMMIT ERROR and PATHS REFUSED in stderr, got:\n%s", errb.String())
	}

	// card-refused MUST NOT have .harvested marker
	if _, err := os.Stat(filepath.Join(refusedJobDir, ".harvested")); !os.IsNotExist(err) {
		t.Fatalf(".harvested marker was unexpectedly created for refused job")
	}

	// card-refused MUST have unchanged scratch/repo/index/branch state
	// Scratch file remains in repo, not moved to scratch-from-repo
	if _, err := os.Stat(refusedScratch); err != nil {
		t.Fatalf("scratch file was unexpectedly moved/deleted in refused repo")
	}
	if _, err := os.Stat(filepath.Join(refusedJobDir, "scratch-from-repo")); !os.IsNotExist(err) {
		t.Fatalf("scratch-from-repo was created for refused job")
	}
	// Branch unchanged
	refusedBranchCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	refusedBranchCmd.Dir = refusedRepo
	if bOut, err := refusedBranchCmd.CombinedOutput(); err != nil || strings.TrimSpace(string(bOut)) != "main" {
		t.Fatalf("refused repo branch changed: %s, err: %v", string(bOut), err)
	}
	// Staging index unchanged
	diffCachedCmd := exec.Command("git", "diff", "--cached")
	diffCachedCmd.Dir = refusedRepo
	if dOut, err := diffCachedCmd.CombinedOutput(); err != nil || len(strings.TrimSpace(string(dOut))) > 0 {
		t.Fatalf("refused repo index changed: %s", string(dOut))
	}
	// Unauthorized file still uncommitted
	logCmd := exec.Command("git", "log", "-1", "--format=%s")
	logCmd.Dir = refusedRepo
	if lOut, _ := logCmd.CombinedOutput(); strings.Contains(string(lOut), "card-refused") {
		t.Fatalf("refused repo committed unexpected work: %s", string(lOut))
	}

	// card-allowed (paired success) MUST succeed:
	// 1. .harvested marker created
	if _, err := os.Stat(filepath.Join(allowedJobDir, ".harvested")); err != nil {
		t.Fatalf(".harvested marker was not created for allowed job: %v", err)
	}
	// 2. COMMITTED verdict emitted
	if !strings.Contains(out.String(), "COMMITTED card-allowed rowan/card-allowed") {
		t.Errorf("stdout missing COMMITTED card-allowed:\n%s", out.String())
	}
	// 3. Scratch file moved to scratch-from-repo
	if _, err := os.Stat(filepath.Join(allowedJobDir, "scratch-from-repo", "notes.txt")); err != nil {
		t.Fatalf("scratch file was not moved to scratch-from-repo for allowed job")
	}
	if _, err := os.Stat(allowedScratch); !os.IsNotExist(err) {
		t.Fatalf("scratch file remained in repo for allowed job")
	}
	// 4. Committed work in HEAD
	allowedLogCmd := exec.Command("git", "log", "-1", "--format=%s")
	allowedLogCmd.Dir = allowedRepo
	if lOut, _ := allowedLogCmd.CombinedOutput(); !strings.Contains(string(lOut), "RESULT card-allowed") {
		t.Fatalf("allowed job did not commit work: %s", string(lOut))
	}
}
