package land_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
)

// TestLDeterministicTrain verifies that two independent workers produce the identical train sha
// (spec 5.3, B6: "two workers produce one train sha").
func TestLDeterministicTrain(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()

	// 1. Create a bare git repository as the mirror
	bareDir := filepath.Join(tmp, "mirror.git")
	if err := exec.Command("git", "init", "--bare", bareDir).Run(); err != nil {
		t.Fatalf("git init bare: %v", err)
	}

	// 2. In a work tree, create initial commit (fromTip) and member commits
	workDir := filepath.Join(tmp, "work")
	if err := exec.Command("git", "clone", bareDir, workDir).Run(); err != nil {
		t.Fatalf("git clone: %v", err)
	}

	runGit := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	// Initial commit: base
	_ = os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Base\n"), 0644)
	runGit(workDir, "add", "README.md")
	runGit(workDir, "commit", "-m", "initial commit")
	runGit(workDir, "push", "origin", "HEAD:main")
	fromTip := runGit(workDir, "rev-parse", "HEAD")

	// Member 1 commit
	runGit(workDir, "checkout", "-b", "m1")
	_ = os.WriteFile(filepath.Join(workDir, "file1.txt"), []byte("member 1 change\n"), 0644)
	runGit(workDir, "add", "file1.txt")
	runGit(workDir, "commit", "-m", "member 1")
	runGit(workDir, "push", "origin", "HEAD:m1")
	m1Head := runGit(workDir, "rev-parse", "HEAD")

	// Member 2 commit (branched from base)
	runGit(workDir, "checkout", "-b", "m2", fromTip)
	_ = os.WriteFile(filepath.Join(workDir, "file2.txt"), []byte("member 2 change\n"), 0644)
	runGit(workDir, "add", "file2.txt")
	runGit(workDir, "commit", "-m", "member 2")
	runGit(workDir, "push", "origin", "HEAD:m2")
	m2Head := runGit(workDir, "rev-parse", "HEAD")

	members := []string{m1Head, m2Head}
	const batchID = "batch-test-train"
	const createdAt = "1790252098"

	// Worker 1 builds train in bareDir
	train1, err := land.BuildTrain(ctx, land.TrainParams{
		GitDir:    bareDir,
		FromTip:   fromTip,
		Members:   members,
		BatchID:   batchID,
		CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("worker 1 build train: %v", err)
	}

	// Worker 2 in an independent clone/mirror builds train with identical inputs
	clone2 := filepath.Join(tmp, "mirror2.git")
	if err := exec.Command("git", "clone", "--bare", bareDir, clone2).Run(); err != nil {
		t.Fatalf("clone bare2: %v", err)
	}

	train2, err := land.BuildTrain(ctx, land.TrainParams{
		GitDir:    clone2,
		FromTip:   fromTip,
		Members:   members,
		BatchID:   batchID,
		CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("worker 2 build train: %v", err)
	}

	// ASSERTION: Two independent workers produce the exact same train sha and tree
	if train1.TrainHead != train2.TrainHead {
		t.Fatalf("train head mismatch: worker1=%s, worker2=%s", train1.TrainHead, train2.TrainHead)
	}
	if train1.TrainTree != train2.TrainTree {
		t.Fatalf("train tree mismatch: worker1=%s, worker2=%s", train1.TrainTree, train2.TrainTree)
	}
	if len(train1.Commits) != 2 || len(train2.Commits) != 2 {
		t.Fatalf("expected 2 intermediate commits, got w1=%d, w2=%d", len(train1.Commits), len(train2.Commits))
	}
	for i := range train1.Commits {
		if train1.Commits[i] != train2.Commits[i] {
			t.Fatalf("commit %d mismatch: %s != %s", i, train1.Commits[i], train2.Commits[i])
		}
	}
}
