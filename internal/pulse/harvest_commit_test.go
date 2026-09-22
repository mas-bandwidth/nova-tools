package pulse

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Table-driven tests for RESULT.md line 2 DONE prefix matching.
func TestIsDoneLineTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		line  string
		match bool
	}{
		// Positive controls
		{"exact DONE", "DONE", true},
		{"DONE with spaces", "   DONE   ", true},
		{"DONE paren message", "DONE (both runs green)", true},
		{"DONE colon message", "DONE: all tests pass", true},
		{"DONE tab message", "DONE\tboth passed", true},
		{"DONE bracket message", "DONE [all green]", true},
		{"DONE semicolon message", "DONE; tests passed", true},
		{"DONE hyphen message", "DONE - green", true},
		{"DONE with lowercase details", "DONE (run 1 ok, run 2 ok)", true},

		// Negative controls
		{"DONEish suffix", "DONEish", false},
		{"DONE_NOT suffix", "DONE_NOT", false},
		{"DONENESS suffix", "DONENESS", false},
		{"DONE123 suffix", "DONE123", false},
		{"ABSTAIN exact", "ABSTAIN", false},
		{"ABSTAIN timeout", "ABSTAIN (timeout on suite)", false},
		{"BLOCKED exact", "BLOCKED", false},
		{"BLOCKED dependency", "BLOCKED (need pr #100)", false},
		{"empty line", "", false},
		{"only whitespace", "    \t   ", false},
		{"not done prefix", "NOT DONE", false},
		{"lowercase done", "done", false},
		{"random text", "fixed the bug", false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := IsDoneLine(tc.line)
			if got != tc.match {
				t.Errorf("IsDoneLine(%q) = %v, want %v", tc.line, got, tc.match)
			}
		})
	}
}

// Table-driven tests for draft-only card label detection.
func TestIsDraftOnlyTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		label string
		draft bool
	}{
		{"fix-nova-tools-2454-r1-1", true},
		{"fix-nova-tools-2416-r1-2", true},
		{"fix-nova-tools-2414-r1-3", true},
		{"fix-nova-tools-2379-r1-4", true},
		{"fix-nova-tools-2425-r1-8", true},
		{"fix-nova-tools-2426-r1-1", true},
		{"fix-nova-tools-2415-r1-1", true},
		{"fix-nova-tools-2459-r1-1", true},
		{"fix-nova-tools-2417-r1-1", true},
		// Negative controls
		{"fix-nova-tools-2000-r1-1", false},
		{"fix-nova-tools-2453-r1-1", false},
		{"fix-nova-tools-2427-r1-1", false},
		{"card-00-fix-nova-tools-100", false},
		{"spec-nova-tools-2454", false},
		{"read-nova-tools-2425", false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			got := IsDraftOnly(tc.label)
			if got != tc.draft {
				t.Errorf("IsDraftOnly(%q) = %v, want %v", tc.label, got, tc.draft)
			}
		})
	}
}

func initTestGitRepo(t *testing.T, dir string) {
	t.Helper()
	runCmd(t, dir, "git", "init", "-b", "main")
	runCmd(t, dir, "git", "config", "user.name", "Rowan")
	runCmd(t, dir, "git", "config", "user.email", "rowan@mas-bandwidth.com")
	initFile := filepath.Join(dir, "init.txt")
	if err := os.WriteFile(initFile, []byte("initial commit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, dir, "git", "add", "init.txt")
	runCmd(t, dir, "git", "commit", "-m", "initial commit")
}

func runCmd(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v in %s failed: %v\n%s", name, args, dir, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

// Positive and negative controls for RESULT.md line 2 DONE check in CommitJob.
func TestCommitJobLine2DoneVerdictsTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		resultBody string
		wantSkip   bool
		skipReason string
	}{
		{
			name:       "valid DONE passes line 2 check",
			resultBody: "RESULT test sha=012345678901\nDONE\nBRANCH rowan/test\n",
			wantSkip:   false,
		},
		{
			name:       "valid DONE with runs green passes",
			resultBody: "RESULT test sha=012345678901\nDONE (both runs green)\nBRANCH rowan/test\n",
			wantSkip:   false,
		},
		{
			name:       "DONEish fails with not-done",
			resultBody: "RESULT test sha=012345678901\nDONEish\nBRANCH rowan/test\n",
			wantSkip:   true,
			skipReason: `reason=not-done line2="DONEish"`,
		},
		{
			name:       "ABSTAIN fails with not-done",
			resultBody: "RESULT test sha=012345678901\nABSTAIN (timeout)\nBRANCH rowan/test\n",
			wantSkip:   true,
			skipReason: `reason=not-done line2="ABSTAIN (timeout)"`,
		},
		{
			name:       "BLOCKED fails with not-done",
			resultBody: "RESULT test sha=012345678901\nBLOCKED\nBRANCH rowan/test\n",
			wantSkip:   true,
			skipReason: `reason=not-done line2="BLOCKED"`,
		},
		{
			name:       "empty line 2 fails with not-done",
			resultBody: "RESULT test sha=012345678901\n",
			wantSkip:   true,
			skipReason: `reason=not-done line2=""`,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			jobDir := filepath.Join(root, "job-1")
			repoDir := filepath.Join(jobDir, "repo")
			if err := os.MkdirAll(repoDir, 0o755); err != nil {
				t.Fatal(err)
			}
			initTestGitRepo(t, repoDir)

			if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(tc.resultBody), 0o644); err != nil {
				t.Fatal(err)
			}

			// Add a changed file so commit would trigger if line 2 passes
			if err := os.WriteFile(filepath.Join(repoDir, "change.txt"), []byte("change\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			var stdout bytes.Buffer
			lines, err := CommitJob(CommitJobInput{
				JobDir: jobDir,
				Stdout: &stdout,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			joined := strings.Join(lines, "\n")
			if tc.wantSkip {
				if !strings.Contains(joined, tc.skipReason) {
					t.Fatalf("output does not contain %q:\n%s", tc.skipReason, joined)
				}
				// Verify no commit was made
				logOut := runCmd(t, repoDir, "git", "log", "-1", "--format=%s")
				if logOut != "initial commit" {
					t.Fatalf("commit was made when line 2 was not DONE: %s", logOut)
				}
			} else {
				if strings.Contains(joined, "reason=not-done") {
					t.Fatalf("unexpected not-done skip in:\n%s", joined)
				}
				if !strings.Contains(joined, "COMMITTED") {
					t.Fatalf("expected COMMITTED line in:\n%s", joined)
				}
			}
		})
	}
}

// Verify that a missing RESULT.md or non-git repo emits SKIP verdicts.
func TestCommitJobMissingResultAndNotGitRepo(t *testing.T) {
	t.Parallel()

	// Missing RESULT.md
	t.Run("missing RESULT.md", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		jobDir := filepath.Join(root, "job-no-res")
		if err := os.MkdirAll(jobDir, 0o755); err != nil {
			t.Fatal(err)
		}
		var stdout bytes.Buffer
		lines, err := CommitJob(CommitJobInput{JobDir: jobDir, Stdout: &stdout})
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "SKIP job-no-res reason=no-result") {
			t.Fatalf("expected reason=no-result, got: %s", joined)
		}
	})

	// Not a git repository
	t.Run("not a git repo", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		jobDir := filepath.Join(root, "job-not-git")
		if err := os.MkdirAll(jobDir, 0o755); err != nil {
			t.Fatal(err)
		}
		res := "RESULT job-not-git sha=012345678901\nDONE\nBRANCH rowan/not-git\n"
		if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(res), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout bytes.Buffer
		lines, err := CommitJob(CommitJobInput{JobDir: jobDir, Stdout: &stdout})
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "SKIP job-not-git reason=not-git-repo") {
			t.Fatalf("expected reason=not-git-repo, got: %s", joined)
		}
	})
}

// Verify 1 MB file refusal: refuse if any changed file is > 1048576 bytes.
func TestCommitJob1MBFileRefusal(t *testing.T) {
	t.Parallel()

	// Positive control: file <= 1 MB (1048576 bytes) commits cleanly
	t.Run("file under 1 MB passes", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		jobDir := filepath.Join(root, "card-00-job-small")
		repoDir := filepath.Join(jobDir, "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatal(err)
		}
		initTestGitRepo(t, repoDir)

		res := "RESULT job-small sha=012345678901\nDONE\nBRANCH rowan/job-small\n"
		if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(res), 0o644); err != nil {
			t.Fatal(err)
		}

		// Write a 1 MB file (exactly 1048576 bytes)
		oneMB := make([]byte, 1048576)
		if err := os.WriteFile(filepath.Join(repoDir, "one_mb.dat"), oneMB, 0o644); err != nil {
			t.Fatal(err)
		}

		var stdout bytes.Buffer
		lines, err := CommitJob(CommitJobInput{JobDir: jobDir, Stdout: &stdout})
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(lines, "\n")
		if strings.Contains(joined, "CLEAN-TREE REFUSED") {
			t.Fatalf("unexpected CLEAN-TREE REFUSED for 1 MB file: %s", joined)
		}
		if !strings.Contains(joined, "COMMITTED job-small rowan/job-small") {
			t.Fatalf("expected COMMITTED line in: %s", joined)
		}
	})

	// Negative control: file > 1 MB (1048577 bytes) is refused
	t.Run("file over 1 MB refused", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		jobDir := filepath.Join(root, "card-00-job-big")
		repoDir := filepath.Join(jobDir, "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatal(err)
		}
		initTestGitRepo(t, repoDir)

		res := "RESULT job-big sha=012345678901\nDONE\nBRANCH rowan/job-big\n"
		if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(res), 0o644); err != nil {
			t.Fatal(err)
		}

		// Write 1 MB + 1 byte (1048577 bytes)
		tooBig := make([]byte, 1048577)
		if err := os.WriteFile(filepath.Join(repoDir, "large_data.bin"), tooBig, 0o644); err != nil {
			t.Fatal(err)
		}

		var stdout bytes.Buffer
		lines, err := CommitJob(CommitJobInput{JobDir: jobDir, Stdout: &stdout})
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(lines, "\n")
		wantRefusal := "CLEAN-TREE REFUSED job-big: large_data.bin (a file over 1 MB; attribution 2026-09-21); left uncommitted"
		if !strings.Contains(joined, wantRefusal) {
			t.Fatalf("expected refusal line %q in:\n%s", wantRefusal, joined)
		}

		// Verify left uncommitted
		logOut := runCmd(t, repoDir, "git", "log", "-1", "--format=%s")
		if logOut != "initial commit" {
			t.Fatalf("big file was committed! Last commit message: %s", logOut)
		}
	})
}

// Verify setting aside card scratch files: RESULT.md, notes.txt, REPORT.md, usage.tsv, harness-output.log, repo.bundle.
func TestCommitJobSetAsideCardScratch(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	jobDir := filepath.Join(root, "card-00-job-scratch")
	repoDir := filepath.Join(jobDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initTestGitRepo(t, repoDir)

	res := "RESULT job-scratch sha=012345678901\nDONE\nBRANCH rowan/scratch\n"
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(res), 0o644); err != nil {
		t.Fatal(err)
	}

	// Put scratch files in repo directory
	scratchInRepo := []string{"RESULT.md", "notes.txt", "REPORT.md", "usage.tsv", "harness-output.log", "repo.bundle"}
	for _, f := range scratchInRepo {
		if err := os.WriteFile(filepath.Join(repoDir, f), []byte("scratch content for "+f+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Also a legitimate source code file
	if err := os.WriteFile(filepath.Join(repoDir, "feature.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	lines, err := CommitJob(CommitJobInput{JobDir: jobDir, Stdout: &stdout})
	if err != nil {
		t.Fatal(err)
	}

	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "SCRATCH-MOVED job-scratch") {
		t.Fatalf("expected SCRATCH-MOVED line in:\n%s", joined)
	}
	for _, f := range scratchInRepo {
		if !strings.Contains(joined, f) {
			t.Errorf("SCRATCH-MOVED line missing file %s in:\n%s", f, joined)
		}
	}
	if !strings.Contains(joined, "COMMITTED job-scratch rowan/scratch") {
		t.Fatalf("expected COMMITTED line in:\n%s", joined)
	}

	// Verify scratch files moved to jobDir/scratch-from-repo
	scratchDir := filepath.Join(jobDir, "scratch-from-repo")
	for _, f := range scratchInRepo {
		movedPath := filepath.Join(scratchDir, f)
		if _, err := os.Stat(movedPath); err != nil {
			t.Errorf("scratch file %s not found in %s", f, scratchDir)
		}
		// Must not be in repoDir
		if _, err := os.Stat(filepath.Join(repoDir, f)); err == nil {
			t.Errorf("scratch file %s still present in repoDir", f)
		}
	}

	// Verify feature.go was committed in HEAD
	showOut := runCmd(t, repoDir, "git", "show", "--name-only", "--format=")
	if !strings.Contains(showOut, "feature.go") {
		t.Fatalf("feature.go was not in the commit:\n%s", showOut)
	}
	for _, f := range scratchInRepo {
		if strings.Contains(showOut, f) {
			t.Fatalf("scratch file %s was committed into repo:\n%s", f, showOut)
		}
	}
}

// Verify checkout -B <branch> and commit message from line 1 of RESULT.md.
func TestCommitJobCheckoutAndCommitMessage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	jobDir := filepath.Join(root, "card-00-job-commit")
	repoDir := filepath.Join(jobDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initTestGitRepo(t, repoDir)

	longTitle := "RESULT: CARD-123 " + strings.Repeat("very long descriptive commit message title ", 10)
	res := fmt.Sprintf("%s\nDONE\nBRANCH: my-feature-branch\n", longTitle)
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(res), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(repoDir, "fix.go"), []byte("package fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	lines, err := CommitJob(CommitJobInput{JobDir: jobDir, Stdout: &stdout})
	if err != nil {
		t.Fatal(err)
	}

	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "COMMITTED job-commit rowan/my-feature-branch") {
		t.Fatalf("expected COMMITTED rowan/my-feature-branch, got:\n%s", joined)
	}

	// Verify branch checked out
	curBranch := runCmd(t, repoDir, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if curBranch != "rowan/my-feature-branch" {
		t.Fatalf("HEAD branch = %q, want rowan/my-feature-branch", curBranch)
	}

	// Verify commit message capped at 200 chars
	msg := runCmd(t, repoDir, "git", "log", "-1", "--format=%B")
	wantMsg := longTitle
	if len(wantMsg) > 200 {
		wantMsg = wantMsg[:200]
	}
	if strings.TrimSpace(msg) != strings.TrimSpace(wantMsg) {
		t.Fatalf("commit message = %q, want %q", msg, wantMsg)
	}
}

// Verify clean tree branch rename when current branch != declared branch.
func TestCommitJobCleanTreeBranchRename(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	jobDir := filepath.Join(root, "card-00-job-rename")
	repoDir := filepath.Join(jobDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initTestGitRepo(t, repoDir)
	// Create and checkout old branch
	runCmd(t, repoDir, "git", "checkout", "-b", "temp-branch")

	res := "RESULT job-rename sha=012345678901\nDONE\nBRANCH rowan/job-rename\n"
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(res), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	lines, err := CommitJob(CommitJobInput{JobDir: jobDir, Stdout: &stdout})
	if err != nil {
		t.Fatal(err)
	}

	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "RENAMED job-rename temp-branch -> rowan/job-rename") {
		t.Fatalf("expected RENAMED line in:\n%s", joined)
	}

	curBranch := runCmd(t, repoDir, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if curBranch != "rowan/job-rename" {
		t.Fatalf("HEAD branch = %q, want rowan/job-rename", curBranch)
	}
}

// Verify scratch drop outside PATHS: base..HEAD scratch files are removed.
func TestCommitJobScratchDrop(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	jobDir := filepath.Join(root, "card-00-job-drop")
	repoDir := filepath.Join(jobDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initTestGitRepo(t, repoDir)
	baseSHA := runCmd(t, repoDir, "git", "rev-parse", "HEAD")

	// Commit some code AND some scratch files
	if err := os.MkdirAll(filepath.Join(repoDir, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoDir, ".nova-sandbox-tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "code.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "notes.txt"), []byte("scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "notes", "todo.txt"), []byte("todo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".nova-sandbox-tmp", "tmp.out"), []byte("tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "App.class"), []byte("class\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "file.o"), []byte("obj\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "file.obj"), []byte("obj\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "file.pyc"), []byte("pyc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "file.exe"), []byte("exe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "a.out"), []byte("binary\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := fmt.Sprintf("RESULT job-drop sha=%s fixed bug\nDONE\nBRANCH rowan/drop\n", baseSHA)
	if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(res), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	lines, err := CommitJob(CommitJobInput{JobDir: jobDir, Stdout: &stdout})
	if err != nil {
		t.Fatal(err)
	}

	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "SCRATCH-DROPPED job-drop") {
		t.Fatalf("expected SCRATCH-DROPPED line in:\n%s", joined)
	}

	// Verify scratch files are dropped from HEAD
	filesInHead := runCmd(t, repoDir, "git", "ls-tree", "-r", "--name-only", "HEAD")
	if !strings.Contains(filesInHead, "code.go") {
		t.Fatalf("code.go should remain in HEAD, got:\n%s", filesInHead)
	}
	droppedCheck := []string{"notes.txt", "notes/todo.txt", ".nova-sandbox-tmp/tmp.out", "App.class", "file.o", "file.obj", "file.pyc", "file.exe", "a.out"}
	for _, f := range droppedCheck {
		if strings.Contains(filesInHead, f) {
			t.Errorf("scratch file %s was NOT dropped from HEAD:\n%s", f, filesInHead)
		}
	}
}

// Verify rebase onto moved target.
func TestCommitJobRebaseOntoMovedTarget(t *testing.T) {
	t.Parallel()

	// 1. Successful rebase when target moved
	t.Run("rebase success", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		remoteDir := filepath.Join(root, "remote.git")
		runCmd(t, root, "git", "init", "--bare", remoteDir)

		// Create seed repo and push to remote dev branch
		seedDir := filepath.Join(root, "seed")
		if err := os.MkdirAll(seedDir, 0o755); err != nil {
			t.Fatal(err)
		}
		initTestGitRepo(t, seedDir)
		runCmd(t, seedDir, "git", "remote", "add", "origin", remoteDir)
		runCmd(t, seedDir, "git", "push", "origin", "main:dev")

		// Create job repo cloned from remote
		jobDir := filepath.Join(root, "card-00-job-rebase")
		repoDir := filepath.Join(jobDir, "repo")
		runCmd(t, root, "git", "clone", "-b", "dev", remoteDir, repoDir)
		runCmd(t, repoDir, "git", "config", "user.name", "Rowan")
		runCmd(t, repoDir, "git", "config", "user.email", "rowan@mas-bandwidth.com")

		// Create card change in repoDir
		if err := os.WriteFile(filepath.Join(repoDir, "card_work.txt"), []byte("worker work\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		res := "RESULT job-rebase sha=012345678901\nDONE\nBRANCH rowan/rebase\n"
		if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(res), 0o644); err != nil {
			t.Fatal(err)
		}

		// Now move the remote target base forward with another commit
		if err := os.WriteFile(filepath.Join(seedDir, "other_pr.txt"), []byte("other pr\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runCmd(t, seedDir, "git", "add", "other_pr.txt")
		runCmd(t, seedDir, "git", "commit", "-m", "other pr landed")
		runCmd(t, seedDir, "git", "push", "origin", "main:dev")

		var stdout bytes.Buffer
		lines, err := CommitJob(CommitJobInput{
			JobDir:      jobDir,
			DefaultBase: "dev",
			Stdout:      &stdout,
		})
		if err != nil {
			t.Fatal(err)
		}

		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "REBASED job-rebase onto dev@") {
			t.Fatalf("expected REBASED line in:\n%s", joined)
		}

		// Verify rebase was clean: HEAD should have other_pr.txt and card_work.txt
		filesInHead := runCmd(t, repoDir, "git", "ls-tree", "-r", "--name-only", "HEAD")
		if !strings.Contains(filesInHead, "other_pr.txt") || !strings.Contains(filesInHead, "card_work.txt") {
			t.Fatalf("expected both files after rebase:\n%s", filesInHead)
		}
	})

	// 2. Conflict handling: rebase conflict aborts and emits REBASE-CONFLICT
	t.Run("rebase conflict", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		remoteDir := filepath.Join(root, "remote-conflict.git")
		runCmd(t, root, "git", "init", "--bare", remoteDir)

		seedDir := filepath.Join(root, "seed-conflict")
		if err := os.MkdirAll(seedDir, 0o755); err != nil {
			t.Fatal(err)
		}
		initTestGitRepo(t, seedDir)
		runCmd(t, seedDir, "git", "remote", "add", "origin", remoteDir)
		runCmd(t, seedDir, "git", "push", "origin", "main:dev")

		jobDir := filepath.Join(root, "card-00-job-conflict")
		repoDir := filepath.Join(jobDir, "repo")
		runCmd(t, root, "git", "clone", "-b", "dev", remoteDir, repoDir)
		runCmd(t, repoDir, "git", "config", "user.name", "Rowan")
		runCmd(t, repoDir, "git", "config", "user.email", "rowan@mas-bandwidth.com")

		// Both modify init.txt in conflicting ways
		if err := os.WriteFile(filepath.Join(repoDir, "init.txt"), []byte("worker conflict\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		res := "RESULT job-conflict sha=012345678901\nDONE\nBRANCH rowan/conflict\n"
		if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(res), 0o644); err != nil {
			t.Fatal(err)
		}

		// Remote modifies init.txt differently
		if err := os.WriteFile(filepath.Join(seedDir, "init.txt"), []byte("remote conflict\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runCmd(t, seedDir, "git", "add", "init.txt")
		runCmd(t, seedDir, "git", "commit", "-m", "remote conflict commit")
		runCmd(t, seedDir, "git", "push", "origin", "main:dev")

		var stdout bytes.Buffer
		lines, err := CommitJob(CommitJobInput{
			JobDir:      jobDir,
			DefaultBase: "dev",
			Stdout:      &stdout,
		})
		if err != nil {
			t.Fatal(err)
		}

		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "REBASE-CONFLICT job-conflict onto dev (author work: recut)") {
			t.Fatalf("expected REBASE-CONFLICT line in:\n%s", joined)
		}

		// Rebase must have been aborted
		status := runCmd(t, repoDir, "git", "status", "--porcelain")
		if status != "" {
			t.Fatalf("repo was left dirty after rebase abort:\n%s", status)
		}
	})
}

// Verify writing BASE, BRANCH, and prior lines into RESULT.md.
func TestCommitJobWriteBaseBranchPriorLines(t *testing.T) {
	t.Parallel()

	// Missing BRANCH line gets written
	t.Run("missing BRANCH written", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		jobDir := filepath.Join(root, "card-00-job-nobranch")
		repoDir := filepath.Join(jobDir, "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatal(err)
		}
		initTestGitRepo(t, repoDir)

		res := "RESULT job-nobranch sha=012345678901\nDONE\n"
		if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(res), 0o644); err != nil {
			t.Fatal(err)
		}

		var stdout bytes.Buffer
		lines, err := CommitJob(CommitJobInput{JobDir: jobDir, Stdout: &stdout})
		if err != nil {
			t.Fatal(err)
		}

		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "BRANCH-LINE job-nobranch rowan/job-nobranch") {
			t.Fatalf("expected BRANCH-LINE in:\n%s", joined)
		}

		// Check file content
		content, _ := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
		if !strings.Contains(string(content), "BRANCH rowan/job-nobranch") {
			t.Fatalf("RESULT.md missing written BRANCH line:\n%s", string(content))
		}
	})

	// Outdated BRANCH line updated
	t.Run("outdated BRANCH updated", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		jobDir := filepath.Join(root, "card-00-job-oldbranch")
		repoDir := filepath.Join(jobDir, "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatal(err)
		}
		initTestGitRepo(t, repoDir)

		res := "RESULT job-oldbranch sha=012345678901\nDONE\nBRANCH: wrong-branch\n"
		if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(res), 0o644); err != nil {
			t.Fatal(err)
		}

		var stdout bytes.Buffer
		lines, err := CommitJob(CommitJobInput{JobDir: jobDir, Stdout: &stdout})
		if err != nil {
			t.Fatal(err)
		}

		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "BRANCH-LINE job-oldbranch -> rowan/wrong-branch") {
			t.Fatalf("expected BRANCH-LINE update in:\n%s", joined)
		}

		content, _ := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
		if !strings.Contains(string(content), "BRANCH rowan/wrong-branch") {
			t.Fatalf("RESULT.md missing updated BRANCH line:\n%s", string(content))
		}
	})

	// Schema repo writes BASE main
	t.Run("schema repo writes BASE main", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		jobDir := filepath.Join(root, "card-00-job-schema")
		repoDir := filepath.Join(jobDir, "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatal(err)
		}
		initTestGitRepo(t, repoDir)

		res := "RESULT job-schema sha=012345678901\nDONE\nREPO mas-bandwidth/schema\nBRANCH rowan/schema-fix\n"
		if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(res), 0o644); err != nil {
			t.Fatal(err)
		}

		var stdout bytes.Buffer
		lines, err := CommitJob(CommitJobInput{JobDir: jobDir, Stdout: &stdout})
		if err != nil {
			t.Fatal(err)
		}

		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "BASE-LINE job-schema main") {
			t.Fatalf("expected BASE-LINE in:\n%s", joined)
		}

		content, _ := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
		if !strings.Contains(string(content), "BASE main") {
			t.Fatalf("RESULT.md missing BASE main:\n%s", string(content))
		}
	})

	// recut-* card writes prior line
	t.Run("recut card writes prior line", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		jobDir := filepath.Join(root, "card-00-recut-nova-tools-2425-r1-8")
		repoDir := filepath.Join(jobDir, "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatal(err)
		}
		initTestGitRepo(t, repoDir)

		// Create dummy ref for refs/pull/2425/head
		headSHA := runCmd(t, repoDir, "git", "rev-parse", "HEAD")
		runCmd(t, repoDir, "git", "update-ref", "refs/pull/2425/head", headSHA)

		res := "RESULT recut-nova-tools-2425-r1-8 sha=012345678901\nDONE\nBRANCH rowan/recut-2425\n"
		if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(res), 0o644); err != nil {
			t.Fatal(err)
		}

		var stdout bytes.Buffer
		lines, err := CommitJob(CommitJobInput{JobDir: jobDir, Stdout: &stdout})
		if err != nil {
			t.Fatal(err)
		}

		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "PRIOR-LINE recut-nova-tools-2425-r1-8 #2425 @") {
			t.Fatalf("expected PRIOR-LINE in:\n%s", joined)
		}

		content, _ := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
		if !strings.Contains(string(content), "prior: #2425 @") {
			t.Fatalf("RESULT.md missing prior: line:\n%s", string(content))
		}
	})
}

// Verify folding REPORT.md into RESULT.md (up to 1500 bytes).
func TestCommitJobFoldReport(t *testing.T) {
	t.Parallel()

	// Positive control: REPORT.md folded into RESULT.md
	t.Run("folds report", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		jobDir := filepath.Join(root, "card-00-job-rep")
		repoDir := filepath.Join(jobDir, "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatal(err)
		}
		initTestGitRepo(t, repoDir)

		res := "RESULT job-rep sha=012345678901\nDONE\nBRANCH rowan/rep\n"
		if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(res), 0o644); err != nil {
			t.Fatal(err)
		}

		repText := "This is a detailed execution report from the swarm worker."
		if err := os.WriteFile(filepath.Join(jobDir, "REPORT.md"), []byte(repText), 0o644); err != nil {
			t.Fatal(err)
		}

		var stdout bytes.Buffer
		lines, err := CommitJob(CommitJobInput{JobDir: jobDir, Stdout: &stdout})
		if err != nil {
			t.Fatal(err)
		}

		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "REPORT-FOLDED job-rep") {
			t.Fatalf("expected REPORT-FOLDED line in:\n%s", joined)
		}

		content, _ := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
		if !strings.Contains(string(content), "--- REPORT (the card, at most 1500 bytes) ---") ||
			!strings.Contains(string(content), repText) {
			t.Fatalf("REPORT.md not folded into RESULT.md:\n%s", string(content))
		}
	})

	// Report over 1500 bytes is capped
	t.Run("caps report at 1500 bytes", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		jobDir := filepath.Join(root, "card-00-job-huge-rep")
		repoDir := filepath.Join(jobDir, "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatal(err)
		}
		initTestGitRepo(t, repoDir)

		res := "RESULT job-huge-rep sha=012345678901\nDONE\nBRANCH rowan/huge-rep\n"
		if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(res), 0o644); err != nil {
			t.Fatal(err)
		}

		longReport := strings.Repeat("x", 2000)
		if err := os.WriteFile(filepath.Join(jobDir, "REPORT.md"), []byte(longReport), 0o644); err != nil {
			t.Fatal(err)
		}

		var stdout bytes.Buffer
		lines, err := CommitJob(CommitJobInput{JobDir: jobDir, Stdout: &stdout})
		if err != nil {
			t.Fatal(err)
		}

		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "REPORT-FOLDED job-huge-rep") {
			t.Fatalf("expected REPORT-FOLDED line in:\n%s", joined)
		}

		content, _ := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
		// Check that the folded section contains exactly 1500 'x' characters
		expectedSection := strings.Repeat("x", 1500)
		if !strings.Contains(string(content), expectedSection) {
			t.Fatalf("expected 1500 'x' in folded section")
		}
		if strings.Contains(string(content), expectedSection+"x") {
			t.Fatalf("folded section exceeded 1500 bytes cap!")
		}
	})

	// Negative control: already folded or has CLAIM line
	t.Run("does not refold if CLAIM exists", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		jobDir := filepath.Join(root, "card-00-job-claim")
		repoDir := filepath.Join(jobDir, "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatal(err)
		}
		initTestGitRepo(t, repoDir)

		res := "RESULT job-claim sha=012345678901\nDONE\nCLAIM: already claimed\nBRANCH rowan/claim\n"
		if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(res), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(jobDir, "REPORT.md"), []byte("rep"), 0o644); err != nil {
			t.Fatal(err)
		}

		var stdout bytes.Buffer
		lines, err := CommitJob(CommitJobInput{JobDir: jobDir, Stdout: &stdout})
		if err != nil {
			t.Fatal(err)
		}

		joined := strings.Join(lines, "\n")
		if strings.Contains(joined, "REPORT-FOLDED") {
			t.Fatalf("unexpected REPORT-FOLDED when CLAIM was present:\n%s", joined)
		}
	})
}

// Verify draft-only and read card skip rules.
func TestCommitJobDraftOnlyAndReadCard(t *testing.T) {
	t.Parallel()

	// Read card skipped
	t.Run("read card skipped", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		jobDir := filepath.Join(root, "card-00-read-pr-1234")
		var stdout bytes.Buffer
		lines, err := CommitJob(CommitJobInput{JobDir: jobDir, Stdout: &stdout})
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "SKIP read-pr-1234 reason=read-card") {
			t.Fatalf("expected SKIP read-card line in:\n%s", joined)
		}
	})

	// Draft-only card skipped with message
	t.Run("draft only card skipped", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		jobDir := filepath.Join(root, "card-00-fix-nova-tools-2425-r1-8")
		var stdout bytes.Buffer
		lines, err := CommitJob(CommitJobInput{JobDir: jobDir, Stdout: &stdout})
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(lines, "\n")
		wantDraft := "DRAFT-ONLY fix-nova-tools-2425-r1-8 (Glenn 2026-09-21: friends build the tooling; the swarm card is a draft, not committed or harvested)"
		if !strings.Contains(joined, wantDraft) {
			t.Fatalf("expected DRAFT-ONLY line in:\n%s", joined)
		}
	})
}

// Verify CommitStep directory traversal, discovery and all verdicts preserved.
func TestCommitStepTraversalAndVerdicts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// 1. A valid DONE job under card-1/jobs/fix-1
	j1 := filepath.Join(root, "card-1", "jobs", "fix-1")
	r1 := filepath.Join(j1, "repo")
	if err := os.MkdirAll(r1, 0o755); err != nil {
		t.Fatal(err)
	}
	initTestGitRepo(t, r1)
	if err := os.WriteFile(filepath.Join(r1, "fix.go"), []byte("fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(j1, "RESULT.md"), []byte("RESULT fix-1 sha=012345678901\nDONE (both runs green)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. An ABSTAIN job under card-2/jobs/fix-2
	j2 := filepath.Join(root, "card-2", "jobs", "fix-2")
	r2 := filepath.Join(j2, "repo")
	if err := os.MkdirAll(r2, 0o755); err != nil {
		t.Fatal(err)
	}
	initTestGitRepo(t, r2)
	if err := os.WriteFile(filepath.Join(j2, "RESULT.md"), []byte("RESULT fix-2 sha=012345678901\nABSTAIN (timeout)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3. A read card under card-3/jobs/read-review
	j3 := filepath.Join(root, "card-3", "jobs", "read-review")
	if err := os.MkdirAll(j3, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(j3, "RESULT.md"), []byte("RESULT read sha=012345678901\nDONE\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	lines, err := CommitStep(CommitStepInput{
		Dir:    root,
		Stdout: &stdout,
	})
	if err != nil {
		t.Fatal(err)
	}

	joined := strings.Join(lines, "\n")
	// Must contain all verdicts, including SKIP lines
	if !strings.Contains(joined, "COMMITTED fix-1 rowan/fix-1") {
		t.Errorf("missing COMMITTED fix-1 in:\n%s", joined)
	}
	if !strings.Contains(joined, `SKIP fix-2 reason=not-done line2="ABSTAIN (timeout)"`) {
		t.Errorf("missing SKIP fix-2 not-done in:\n%s", joined)
	}
	if !strings.Contains(joined, "SKIP read-review reason=read-card") {
		t.Errorf("missing SKIP read-review in:\n%s", joined)
	}

	// Verify stdout matches returned lines
	stdoutLines := strings.TrimSpace(stdout.String())
	returnedLines := strings.TrimSpace(strings.Join(lines, "\n"))
	if stdoutLines != returnedLines {
		t.Fatalf("stdout does not match returned lines:\nstdout:\n%s\nreturned:\n%s", stdoutLines, returnedLines)
	}
}

// Verify that HarvestWorking with Commit: true runs the commit step on an uncommitted DONE job.
func TestHarvestWorkingWithCommitStep(t *testing.T) {
	t.Parallel()

	working := t.TempDir()
	job := wkJob(t, working, "guid-1", "g-commit", "RESULT g-commit sha=012345678901\nDONE\nBRANCH rowan/g-commit\nREPO o/r\n")
	repoDir := filepath.Join(job, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initTestGitRepo(t, repoDir)

	// Add an uncommitted file in the job's repo
	if err := os.WriteFile(filepath.Join(repoDir, "work.go"), []byte("package work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := HarvestWorking(HarvestInput{
		Working: working,
		Commit:  true,
		Clones:  []string{"o/r=" + filepath.Join(t.TempDir(), "coordinator-clone")},
		Stdout:  &stdout,
		Stderr:  &stderr,
		Now:     func() time.Time { return time.Unix(0, 0).UTC() },
	})
	_ = code

	// Verify that the uncommitted file was committed by the commit step
	logOut := runCmd(t, repoDir, "git", "log", "-1", "--format=%s")
	if !strings.Contains(logOut, "RESULT g-commit") {
		t.Fatalf("uncommitted work was not committed when Commit: true, last commit = %q", logOut)
	}
}

// Table-driven tests exercising preconditions before checkout/stage/commit (Finding 2).
func TestCommitJobPreconditionsTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		label          string
		cardRepo       string
		resultLines    []string
		dirtyFile      string
		existingHead   string
		existingBranch string
		clones         []string
		wantVerdict    string
		wantCommitted  bool
	}{
		{
			name:  "positive control: valid branch and PATHS passes and commits",
			label: "job-valid",
			resultLines: []string{
				"RESULT job-valid sha=012345678901",
				"DONE",
				"BRANCH rowan/job-valid",
				"PATHS pkg/valid.go",
			},
			dirtyFile:     "pkg/valid.go",
			wantVerdict:   "COMMITTED job-valid rowan/job-valid",
			wantCommitted: true,
		},
		{
			name:  "negative control: branch is trunk main refused",
			label: "job-trunk-main",
			resultLines: []string{
				"RESULT job-trunk-main sha=012345678901",
				"DONE",
				"BRANCH main",
			},
			dirtyFile:     "file.go",
			wantVerdict:   "BRANCH REFUSED job-trunk-main: branch \"main\" is a trunk",
			wantCommitted: false,
		},
		{
			name:  "negative control: branch is trunk master refused",
			label: "job-trunk-master",
			resultLines: []string{
				"RESULT job-trunk-master sha=012345678901",
				"DONE",
				"BRANCH master",
			},
			dirtyFile:     "file.go",
			wantVerdict:   "BRANCH REFUSED job-trunk-master: branch \"master\" is a trunk",
			wantCommitted: false,
		},
		{
			name:  "negative control: off-prefix branch refused",
			label: "job-badprefix",
			resultLines: []string{
				"RESULT job-badprefix sha=012345678901",
				"DONE",
				"BRANCH attacker/job-badprefix",
			},
			dirtyFile:     "file.go",
			wantVerdict:   "BRANCH REFUSED job-badprefix: branch \"attacker/job-badprefix\" is not under rowan/",
			wantCommitted: false,
		},
		{
			name:  "negative control: branch already exists locally refused",
			label: "job-exists",
			resultLines: []string{
				"RESULT job-exists sha=012345678901",
				"DONE",
				"BRANCH rowan/job-exists",
			},
			dirtyFile:      "file.go",
			existingBranch: "rowan/job-exists",
			wantVerdict:    "BRANCH-RESET REFUSED job-exists: branch rowan/job-exists already exists",
			wantCommitted:  false,
		},
		{
			name:  "negative control: current branch is unexpected worker branch refused",
			label: "job-unexp",
			resultLines: []string{
				"RESULT job-unexp sha=012345678901",
				"DONE",
				"BRANCH rowan/job-unexp",
			},
			dirtyFile:     "file.go",
			existingHead:  "rowan/other-worker",
			wantVerdict:   "BRANCH REFUSED job-unexp: current branch rowan/other-worker is an unexpected branch",
			wantCommitted: false,
		},
		{
			name:  "negative control: modified file outside declared PATHS refused",
			label: "job-badpaths",
			resultLines: []string{
				"RESULT job-badpaths sha=012345678901",
				"DONE",
				"BRANCH rowan/job-badpaths",
				"PATHS pkg/allowed.go",
			},
			dirtyFile:     "unauthorized/intruder.go",
			wantVerdict:   "PATHS REFUSED job-badpaths: unauthorized/intruder.go",
			wantCommitted: false,
		},
		{
			name:     "negative control: claimed repo does not match card repo",
			label:    "job-repomismatch",
			cardRepo: "mas-bandwidth/nova-tools",
			resultLines: []string{
				"RESULT job-repomismatch sha=012345678901",
				"DONE",
				"BRANCH rowan/job-repomismatch",
				"REPO mas-bandwidth/other-repo",
			},
			dirtyFile:     "file.go",
			wantVerdict:   "REPO REFUSED job-repomismatch: claim mas-bandwidth/other-repo does not match card repo mas-bandwidth/nova-tools",
			wantCommitted: false,
		},
		{
			name:   "negative control: claimed repo not in coordinator clone list",
			label:  "job-notdecl",
			clones: []string{"mas-bandwidth/nova-tools=/some/path"},
			resultLines: []string{
				"RESULT job-notdecl sha=012345678901",
				"DONE",
				"BRANCH rowan/job-notdecl",
				"REPO mas-bandwidth/unlisted-repo",
			},
			dirtyFile:     "file.go",
			wantVerdict:   "REPO REFUSED job-notdecl: claim mas-bandwidth/unlisted-repo is not in coordinator clone list",
			wantCommitted: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			jobDir := filepath.Join(root, "card-00-"+tc.label)
			repoDir := filepath.Join(jobDir, "repo")
			if err := os.MkdirAll(repoDir, 0o755); err != nil {
				t.Fatal(err)
			}
			initTestGitRepo(t, repoDir)

			if tc.existingBranch != "" {
				runCmd(t, repoDir, "git", "branch", tc.existingBranch)
			}
			if tc.existingHead != "" {
				runCmd(t, repoDir, "git", "checkout", "-b", tc.existingHead)
			}

			if tc.cardRepo != "" {
				cardContent := fmt.Sprintf("# Card\nREPO %s\n", tc.cardRepo)
				if err := os.WriteFile(filepath.Join(jobDir, "card.md"), []byte(cardContent), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			resContent := strings.Join(tc.resultLines, "\n") + "\n"
			if err := os.WriteFile(filepath.Join(jobDir, "RESULT.md"), []byte(resContent), 0o644); err != nil {
				t.Fatal(err)
			}

			if tc.dirtyFile != "" {
				fullPath := filepath.Join(repoDir, tc.dirtyFile)
				if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(fullPath, []byte("package test\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			var stdout bytes.Buffer
			lines, err := CommitJob(CommitJobInput{
				JobDir: jobDir,
				Clones: tc.clones,
				Stdout: &stdout,
			})
			if err != nil {
				t.Fatalf("unexpected error from CommitJob: %v", err)
			}
			joined := strings.Join(lines, "\n")
			if !strings.Contains(joined, tc.wantVerdict) {
				t.Fatalf("expected verdict %q in output:\n%s", tc.wantVerdict, joined)
			}
			if !tc.wantCommitted && strings.Contains(joined, "COMMITTED") {
				t.Fatalf("COMMITTED emitted when precondition should have refused:\n%s", joined)
			}
		})
	}
}

type mockFailingRunner struct {
	failOnSubstr string
	failErr      error
	underlying   GitRunner
}

func (m mockFailingRunner) Run(dir string, args ...string) (string, error) {
	cmdStr := strings.Join(args, " ")
	if strings.Contains(cmdStr, m.failOnSubstr) {
		return "", m.failErr
	}
	if m.underlying != nil {
		return m.underlying.Run(dir, args...)
	}
	return defaultGitRunner{}.Run(dir, args...)
}

// Table-driven tests exercising error propagation in CommitJob (Finding 3).
func TestCommitJobErrorPropagationTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		failSubstr  string
		readOnlyRes bool
		wantErrSub  string
	}{
		{
			name:       "checkout failure propagates error and prevents COMMITTED",
			failSubstr: "checkout",
			wantErrSub: "checkout -b",
		},
		{
			name:       "git add failure propagates error and prevents COMMITTED",
			failSubstr: "add",
			wantErrSub: "git add -A",
		},
		{
			name:       "git commit failure propagates error and prevents COMMITTED",
			failSubstr: "commit",
			wantErrSub: "git commit",
		},
		{
			name:       "git rev-parse HEAD failure propagates error and prevents COMMITTED",
			failSubstr: "rev-parse --short HEAD",
			wantErrSub: "git rev-parse HEAD",
		},
		{
			name:        "RESULT.md write failure propagates error and prevents false success",
			readOnlyRes: true,
			wantErrSub:  "RESULT.md",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			jobDir := filepath.Join(root, "job-err")
			repoDir := filepath.Join(jobDir, "repo")
			if err := os.MkdirAll(repoDir, 0o755); err != nil {
				t.Fatal(err)
			}
			initTestGitRepo(t, repoDir)

			resPath := filepath.Join(jobDir, "RESULT.md")
			resContent := "RESULT job-err sha=012345678901\nDONE\nBRANCH rowan/job-err\n"
			if err := os.WriteFile(resPath, []byte(resContent), 0o644); err != nil {
				t.Fatal(err)
			}

			// Add uncommitted work
			if err := os.WriteFile(filepath.Join(repoDir, "change.txt"), []byte("change\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			var runner GitRunner = defaultGitRunner{}
			if tc.failSubstr != "" {
				runner = mockFailingRunner{
					failOnSubstr: tc.failSubstr,
					failErr:      errors.New("injected git failure"),
				}
			}

			if tc.readOnlyRes {
				// Add REPORT.md so CommitJob is guaranteed to attempt writing folded report to RESULT.md
				if err := os.WriteFile(filepath.Join(jobDir, "REPORT.md"), []byte("report contents\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(resPath, 0o400); err != nil {
					t.Fatal(err)
				}
				if runtime.GOOS != "windows" {
					if err := os.Chmod(jobDir, 0o555); err != nil {
						t.Fatal(err)
					}
					defer os.Chmod(jobDir, 0o755)
				}
			}

			var stdout bytes.Buffer
			lines, err := CommitJob(CommitJobInput{
				JobDir: jobDir,
				Runner: runner,
				Stdout: &stdout,
			})

			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrSub)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Errorf("expected error %q to contain %q", err.Error(), tc.wantErrSub)
			}
			joined := strings.Join(lines, "\n")
			if strings.Contains(joined, "COMMITTED") {
				t.Fatalf("COMMITTED was emitted despite error:\n%s", joined)
			}
		})
	}
}

// Test CommitStep error propagation when a job fails (Finding 3).
func TestCommitStepErrorPropagation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	j1 := filepath.Join(root, "card-1", "jobs", "fix-1")
	r1 := filepath.Join(j1, "repo")
	if err := os.MkdirAll(r1, 0o755); err != nil {
		t.Fatal(err)
	}
	initTestGitRepo(t, r1)
	if err := os.WriteFile(filepath.Join(r1, "work.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(j1, "RESULT.md"), []byte("RESULT fix-1 sha=012345678901\nDONE\nBRANCH rowan/fix-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := mockFailingRunner{
		failOnSubstr: "commit",
		failErr:      errors.New("injected commit crash"),
	}

	var stdout, stderr bytes.Buffer
	lines, err := CommitStep(CommitStepInput{
		Dir:    root,
		Runner: runner,
		Stdout: &stdout,
		Stderr: &stderr,
	})

	if err == nil {
		t.Fatalf("expected error from CommitStep, got nil")
	}
	if !strings.Contains(err.Error(), "git commit") {
		t.Errorf("expected error to mention git commit, got %v", err)
	}
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "COMMITTED fix-1") {
		t.Fatalf("COMMITTED emitted despite failure:\n%s", joined)
	}
	if !strings.Contains(stderr.String(), "git commit") {
		t.Errorf("stderr does not contain error: %s", stderr.String())
	}
}

// Integration tests verifying HarvestWorking return codes and error propagation (Finding 1 & 3).
func TestHarvestWorkingReturnCodeAndErrorPropagation(t *testing.T) {
	t.Run("positive control: green run returns exit code 0", func(t *testing.T) {
		working := t.TempDir()
		job := wkJob(t, working, "guid-1", "g-ok", "RESULT g-ok sha=012345678901\nDONE\nBRANCH rowan/g-ok\nREPO o/r\n")
		repoDir := filepath.Join(job, "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatal(err)
		}
		initTestGitRepo(t, repoDir)
		if err := os.WriteFile(filepath.Join(repoDir, "ok.go"), []byte("package ok\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		specs := fakePATH(t)
		arglog := filepath.Join(working, "argv.log")
		fakeTool(t, specs, "git", fakeSpec{Log: arglog, Rules: []fakeRule{
			{Arg: 1, Equals: "log", Stdout: "0123456789abcdef0123456789abcdef01234567 2026-09-17T10:00:00+00:00"},
			{Arg: 1, Equals: "ls-remote", Stdout: "0123456789abcdef0123456789abcdef01234567\trefs/heads/rowan/g-ok"},
			originRule("o/r"),
			pinRule(),
		}})
		fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{
			{Arg: 2, Equals: "list", Stdout: "[]"},
			{Arg: 2, Equals: "create", Stdout: "https://example.invalid/o/r/pull/1"},
		}})

		var stdout, stderr bytes.Buffer
		code := HarvestWorking(HarvestInput{
			Working: working,
			Commit:  true,
			Base:    "0000000000000000000000000000000000000000",
			Clones:  []string{"o/r=" + filepath.Join(t.TempDir(), "coordinator-clone")},
			Stdout:  &stdout,
			Stderr:  &stderr,
			Now:     func() time.Time { return time.Unix(0, 0).UTC() },
		})
		if code != 0 {
			t.Fatalf("expected exit code 0 for green run, got %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
		}
	})

	t.Run("negative control: failed commit causes exit code 1 and halts before push", func(t *testing.T) {
		working := t.TempDir()
		job := wkJob(t, working, "guid-2", "g-fail", "RESULT g-fail sha=012345678901\nDONE\nBRANCH rowan/g-fail\nREPO o/r\n")
		repoDir := filepath.Join(job, "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatal(err)
		}
		initTestGitRepo(t, repoDir)

		// Make RESULT.md read-only and add REPORT.md so writing folded report in CommitJob fails
		if err := os.WriteFile(filepath.Join(repoDir, "change.go"), []byte("package change\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(job, "REPORT.md"), []byte("report\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		resPath := filepath.Join(job, "RESULT.md")
		if err := os.Chmod(resPath, 0o400); err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(job, 0o555); err != nil {
				t.Fatal(err)
			}
			defer os.Chmod(job, 0o755)
		}

		var stdout, stderr bytes.Buffer
		code := HarvestWorking(HarvestInput{
			Working: working,
			Commit:  true,
			Clones:  []string{"o/r=" + filepath.Join(t.TempDir(), "coordinator-clone")},
			Stdout:  &stdout,
			Stderr:  &stderr,
			Now:     func() time.Time { return time.Unix(0, 0).UTC() },
		})
		if code != 1 {
			t.Fatalf("expected exit code 1 for failed CommitJob, got %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
		}

		// Verify error was logged to stderr
		if !strings.Contains(stderr.String(), "HARVEST COMMIT ERROR") {
			t.Errorf("expected HARVEST COMMIT ERROR in stderr:\n%s", stderr.String())
		}

		// Verify no .harvested marker was created
		if _, err := os.Stat(filepath.Join(job, ".harvested")); !os.IsNotExist(err) {
			t.Fatalf(".harvested marker was unexpectedly created for failed job")
		}
	})
}

// Tests verifying defaultGitRunner separates stdout and stderr (Finding 4).
func TestDefaultGitRunnerSeparateStdoutStderrTable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	initTestGitRepo(t, root)

	runner := defaultGitRunner{}

	t.Run("warning on success returns clean stdout and nil error", func(t *testing.T) {
		t.Parallel()
		// 'git checkout -b new-branch' writes 'Switched to a new branch ...' to stderr,
		// but stdout is empty and exit code is 0.
		out, err := runner.Run(root, "checkout", "-b", "separate-branch")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// stdout must be clean without stderr contamination
		if out != "" {
			t.Fatalf("expected clean empty stdout, got %q (stderr contaminated stdout)", out)
		}
	})

	t.Run("failure on non-zero exit preserves exit error and stderr text", func(t *testing.T) {
		t.Parallel()
		// Running checkout on nonexistent branch exits non-zero and prints to stderr
		out, err := runner.Run(root, "checkout", "nonexistent-branch-xyz")
		if err == nil {
			t.Fatal("expected error for nonexistent branch checkout, got nil")
		}
		if !strings.Contains(err.Error(), "did not match any file") && !strings.Contains(err.Error(), "pathspec") {
			t.Errorf("error %q does not contain stderr text", err.Error())
		}
		_ = out
	})
}
