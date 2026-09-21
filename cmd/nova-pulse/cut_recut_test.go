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

func runCut(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(append([]string{"cut"}, args...), &stdout, &stderr, time.Now())
	return code, stdout.String(), stderr.String()
}

func setupCLIGitRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", repo, "init", "-q").Run()
	exec.Command("git", "-C", repo, "config", "user.email", "emma@mas-bandwidth.com").Run()
	exec.Command("git", "-C", repo, "config", "user.name", "Emma Antigravity").Run()
	_ = os.WriteFile(filepath.Join(repo, "doc.txt"), []byte("v1\nv2\nv3\n"), 0o644)
	exec.Command("git", "-C", repo, "add", "doc.txt").Run()
	exec.Command("git", "-C", repo, "commit", "-m", "base doc").Run()
	return repo
}

// TestCmdCutKindRecutAppliesClean tests the CLI flag handling of `cut --kind recut --diff-file <f> --dir <d>`.
func TestCmdCutKindRecutAppliesClean(t *testing.T) {
	repo := setupCLIGitRepo(t)
	dir := t.TempDir()
	out, queue := filepath.Join(dir, "pending"), filepath.Join(dir, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}

	exec.Command("git", "-C", repo, "checkout", "-b", "patch-br").Run()
	_ = os.WriteFile(filepath.Join(repo, "doc.txt"), []byte("v1\nv2 patched\nv3\n"), 0o644)
	exec.Command("git", "-C", repo, "commit", "-am", "patch doc").Run()
	diffBytes, _ := exec.Command("git", "-C", repo, "diff", "HEAD~1").Output()
	diffFile := filepath.Join(dir, "doc.diff")
	_ = os.WriteFile(diffFile, diffBytes, 0o644)

	exec.Command("git", "-C", repo, "checkout", "master").Run()
	_ = os.WriteFile(filepath.Join(repo, "other.txt"), []byte("unrelated\n"), 0o644)
	exec.Command("git", "-C", repo, "add", "other.txt").Run()
	exec.Command("git", "-C", repo, "commit", "-m", "unrelated commit").Run()

	code, stdout, stderr := runCut(t,
		"--kind", "recut",
		"--repo", "mas-bandwidth/nova-tools",
		"--diff-file", diffFile,
		"--dir", repo,
		"--title", "clean mechanical recut",
		"--out", out,
		"--queue", queue,
	)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	if !strings.Contains(stdout, "CUT CARD card=card-1.md") || !strings.Contains(stdout, "kind=recut") {
		t.Errorf("stdout = %q", stdout)
	}

	cardBytes, err := os.ReadFile(filepath.Join(out, "card-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	card := string(cardBytes)
	if !strings.Contains(card, "applied: clean\n") {
		t.Errorf("card missing applied: clean:\n%s", card)
	}
}
