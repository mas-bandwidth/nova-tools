package swarm

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitT runs git in dir (empty dir means the current one) with a fixed identity, so the
// tests never touch the network and never depend on a developer's git config.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_TERMINAL_PROMPT=0",
	)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, errb.String())
	}
	return strings.TrimSpace(out.String())
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pull-prefers-the-bench-that-holds-the-repo: nova-swarm pull prefers the card whose repo
// the bench already holds in a kept worktree, and falls back to the bench mirror when no
// bench is warm.
func TestPullPrefersTheBenchThatHoldsTheRepo(t *testing.T) {
	root := t.TempDir()
	coldSlot := filepath.Join(root, "bench-a")
	warmSlot := filepath.Join(root, "bench-b")

	// bench-b already holds acme/warm in a kept worktree; bench-a holds nothing.
	warmWT := WorktreePath(warmSlot, "acme/warm")
	if err := os.MkdirAll(warmWT, 0o755); err != nil {
		t.Fatal(err)
	}
	if !HoldsRepo(warmSlot, "acme/warm") {
		t.Fatalf("HoldsRepo(%q, acme/warm) = false, want true", warmSlot)
	}
	if HoldsRepo(coldSlot, "acme/warm") {
		t.Fatalf("HoldsRepo(%q, acme/warm) = true, want false", coldSlot)
	}

	benches := []PullBench{
		{Name: "a", Slot: coldSlot, Mirror: "/mirror/nova-tools.git",
			Queue: []Card{{Name: "cold", Kind: "go", Repo: "acme/cold"}}},
		{Name: "b", Slot: warmSlot, Mirror: "/mirror/nova-tools.git",
			Queue: []Card{{Name: "warm", Kind: "go", Repo: "acme/warm"}}},
	}
	got, err := Prefer(benches)
	if err != nil {
		t.Fatalf("Prefer: %v", err)
	}
	if got.Bench != "b" || got.Card.Name != "warm" || !got.Warm {
		t.Fatalf("Prefer = bench=%q card=%q warm=%t, want the warm bench b", got.Bench, got.Card.Name, got.Warm)
	}
	if got.Path != warmWT {
		t.Fatalf("Prefer path = %q, want the kept worktree %q", got.Path, warmWT)
	}

	// With no warm bench, pull falls back to the first card and a fetch from the mirror.
	cold, err := Prefer([]PullBench{{Name: "a", Slot: coldSlot, Mirror: "/mirror/nova-tools.git",
		Queue: []Card{{Name: "cold", Kind: "go", Repo: "acme/cold"}}}})
	if err != nil {
		t.Fatalf("Prefer cold: %v", err)
	}
	if cold.Warm || cold.Card.Name != "cold" || cold.Fetch != "/mirror/nova-tools.git" {
		t.Fatalf("cold Prefer = warm=%t card=%q fetch=%q, want a fetch from the bench mirror", cold.Warm, cold.Card.Name, cold.Fetch)
	}
}

// a-clip-resets-the-worktree-to-base: nova-work clip commits the card's branch, harvests
// the card's result, and resets the kept worktree to base, so the next card never sees the
// card's uncommitted state or its history.
func TestAClipResetsTheWorktreeToBase(t *testing.T) {
	root := t.TempDir()

	// Clip shells out to git, which inherits the test process environment; give it an
	// identity so the commit never depends on a developer's git config.
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")

	// A fake git remote: a bare repository on disk, never the network.
	remote := filepath.Join(root, "remote.git")
	gitT(t, "", "init", "--bare", "-q", remote)

	seed := filepath.Join(root, "seed")
	gitT(t, "", "clone", "-q", remote, seed)
	gitT(t, seed, "checkout", "-q", "-b", "dev")
	mustWrite(t, filepath.Join(seed, "base.txt"), "base\n")
	gitT(t, seed, "add", "-A")
	gitT(t, seed, "commit", "-q", "-m", "base")
	gitT(t, seed, "push", "-q", "origin", "dev")

	wt := filepath.Join(root, "worktrees", "acme", "nova-tools")
	gitT(t, "", "clone", "-q", remote, wt)
	gitT(t, wt, "checkout", "-q", "dev")
	gitT(t, wt, "checkout", "-q", "-b", "rowan/card-9302")

	// The card leaves its work uncommitted and its result beside it.
	mustWrite(t, filepath.Join(wt, "work.txt"), "the card's work\n")
	mustWrite(t, filepath.Join(wt, "RESULT.md"), "RESULT: CARD-9302 the card's result\n")

	harvest := filepath.Join(root, "harvest")
	got, err := Clip(ClipRequest{
		Worktree: wt,
		Branch:   "rowan/card-9302",
		Base:     "dev",
		Message:  "card card-9302",
		Result:   "RESULT.md",
		Harvest:  harvest,
	})
	if err != nil {
		t.Fatalf("Clip: %v", err)
	}
	if got.Commit == "" {
		t.Fatalf("Clip commit is empty, want the card's committed tip")
	}

	// The card's branch is committed and one commit ahead of base.
	if ahead := gitT(t, wt, "rev-list", "--count", "dev..rowan/card-9302"); ahead != "1" {
		t.Fatalf("dev..rowan/card-9302 = %s commits ahead, want 1", ahead)
	}
	if head := gitT(t, wt, "rev-parse", "rowan/card-9302"); head != got.Commit {
		t.Fatalf("card branch tip = %s, want the clip commit %s", head, got.Commit)
	}

	// The result is harvested OUT of the worktree.
	harvested := filepath.Join(harvest, "RESULT.md")
	if got.Harvested != harvested {
		t.Fatalf("harvested = %q, want %q", got.Harvested, harvested)
	}
	if body, err := os.ReadFile(harvested); err != nil || !strings.Contains(string(body), "CARD-9302") {
		t.Fatalf("harvested result = %q, err = %v, want the card's result", body, err)
	}

	// The worktree is reset to base: on dev, clean, and the card's files are gone.
	if head := gitT(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); head != "dev" {
		t.Fatalf("worktree HEAD = %q, want base dev", head)
	}
	if status := gitT(t, wt, "status", "--porcelain"); status != "" {
		t.Fatalf("worktree is not clean after clip: %q", status)
	}
	for _, gone := range []string{"work.txt", "RESULT.md"} {
		if _, err := os.Stat(filepath.Join(wt, gone)); !os.IsNotExist(err) {
			t.Fatalf("%s survived the clip, so the next card would see it", gone)
		}
	}
}
