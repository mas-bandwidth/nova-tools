package swarm

import (
	"os"
	"path/filepath"
	"testing"
)

// pull-prefers-the-bench-that-holds-the-repo: nova-swarm pull prefers the card whose repo
// the bench already holds in a kept worktree, and falls back to the bench mirror when no
// bench is warm.
func TestPullPrefersTheBenchThatHoldsTheRepo(t *testing.T) {
	t.Parallel()

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
