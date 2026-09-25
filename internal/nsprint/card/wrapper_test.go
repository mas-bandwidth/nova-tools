package card

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupTestRepo(t *testing.T) (string, string) {
	tmp, err := os.MkdirTemp("", "nsprint-test-*")
	if err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(tmp, "repo")
	os.MkdirAll(repoDir, 0755)

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %s (%v)", args, string(out), err)
		}
	}

	run("init", "-b", "main")
	run("config", "user.name", "Test")
	run("config", "user.email", "test@test.com")

	// Base commit with check.sh, main_test.go, and want_test.go
	checkScript := `grep -q ok want_test.go 2>/dev/null && [ -f fix.txt ] && echo PASS || { echo FAIL; exit 1; }`
	os.WriteFile(filepath.Join(repoDir, "check.sh"), []byte(checkScript), 0755)
	os.WriteFile(filepath.Join(repoDir, "main_test.go"), []byte("package main\n"), 0644)
	os.WriteFile(filepath.Join(repoDir, "want_test.go"), []byte("ok\n"), 0644)
	run("add", "check.sh", "main_test.go", "want_test.go")
	run("commit", "-m", "base")

	baseShaBytes, _ := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD").Output()
	baseSha := strings.TrimSpace(string(baseShaBytes))

	// Head branch adding fix.txt (no *_test.go change)
	run("checkout", "-b", "feature")
	os.WriteFile(filepath.Join(repoDir, "fix.txt"), []byte("fixed\n"), 0644)
	run("add", "fix.txt")
	run("commit", "-m", "feature commit")

	return repoDir, baseSha
}

func TestWrapperCheckReceiptBeforeEnd(t *testing.T) {
	repoDir, baseSha := setupTestRepo(t)
	defer os.RemoveAll(filepath.Dir(repoDir))

	// Add a feat_test.go in feature branch so test-only patch is non-empty
	exec.Command("git", "-C", repoDir, "checkout", "feature").Run()
	os.WriteFile(filepath.Join(repoDir, "feat_test.go"), []byte("package main\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", "feat_test.go").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "add test").Run()

	resultsDir := filepath.Join(filepath.Dir(repoDir), "results")
	card := WrapperCard{
		Identity: "test-card-1",
		Kind:     "fix",
		BaseSha:  baseSha,
		Check:    "sh check.sh",
		Expect:   "(?m)^PASS$",
	}

	outcome, reason, receiptSha, err := RunWrapper(card, repoDir, "feature", resultsDir, 5*time.Second)
	if err != nil {
		t.Fatalf("RunWrapper failed: %v", err)
	}
	if outcome != "DONE" || reason != "done" {
		t.Errorf("expected DONE done, got %s %s", outcome, reason)
	}
	if receiptSha == "-" || len(receiptSha) != 64 {
		t.Errorf("expected valid receipt sha, got %s", receiptSha)
	}

	if _, err := os.Stat(filepath.Join(resultsDir, "check.receipt")); err != nil {
		t.Errorf("expected check.receipt to exist before End")
	}
}

func TestWrapperNotRedFirstEndsBlockedSpec(t *testing.T) {
	repoDir, baseSha := setupTestRepo(t)
	defer os.RemoveAll(filepath.Dir(repoDir))

	// Head branch "feature" only adds fix.txt (no *_test.go diff from base)
	resultsDir := filepath.Join(filepath.Dir(repoDir), "results")
	card := WrapperCard{
		Identity: "test-card-empty",
		Kind:     "fix",
		BaseSha:  baseSha,
		Check:    "sh check.sh",
		Expect:   "(?m)^PASS$",
	}

	outcome, reason, _, err := RunWrapper(card, repoDir, "feature", resultsDir, 5*time.Second)
	if err != nil {
		t.Fatalf("RunWrapper failed: %v", err)
	}
	if outcome != "BLOCKED" || reason != "spec" {
		t.Errorf("expected BLOCKED spec for empty test-only patch, got %s %s", outcome, reason)
	}
}

func TestWrapperBaseRunNotCompletedIsNotRed(t *testing.T) {
	repoDir, baseSha := setupTestRepo(t)
	defer os.RemoveAll(filepath.Dir(repoDir))

	// Add feat_test.go so test-only patch is non-empty
	exec.Command("git", "-C", repoDir, "checkout", "feature").Run()
	os.WriteFile(filepath.Join(repoDir, "feat_test.go"), []byte("package main\n"), 0644)
	exec.Command("git", "-C", repoDir, "add", "feat_test.go").Run()
	exec.Command("git", "-C", repoDir, "commit", "-m", "add test").Run()

	resultsDir := filepath.Join(filepath.Dir(repoDir), "results")
	card := WrapperCard{
		Identity: "test-card-timeout",
		Kind:     "fix",
		BaseSha:  baseSha,
		Check:    "if [ -n \"${BASE_RUN:-}\" ]; then exit 2; else echo PASS; fi",
		Expect:   "(?m)^PASS$",
	}

	outcome, reason, _, err := RunWrapper(card, repoDir, "feature", resultsDir, 5*time.Second)
	if err != nil {
		t.Fatalf("RunWrapper failed: %v", err)
	}
	if outcome != "BLOCKED" || reason != "env" {
		t.Errorf("expected BLOCKED env for base exit 2, got %s %s", outcome, reason)
	}
}
