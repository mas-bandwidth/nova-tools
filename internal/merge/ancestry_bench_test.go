package merge

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupBenchRepo(tb testing.TB) (string, *Git, string, string) {
	tb.Helper()
	dir := tb.TempDir()
	g := NewGit(dir, time.Minute, nil)

	for _, args := range [][]string{
		{"init", "--quiet", "-b", "main", "."},
		{"config", "user.name", "Nova Benchmark"},
		{"config", "user.email", "benchmark@example.invalid"},
	} {
		if out, err := g.Run(args...); err != nil {
			tb.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	initFile := filepath.Join(dir, "initial.txt")
	if err := os.WriteFile(initFile, []byte("initial\n"), 0o644); err != nil {
		tb.Fatalf("write initial.txt: %v", err)
	}
	if out, err := g.Run("add", "initial.txt"); err != nil {
		tb.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := g.Run("commit", "--quiet", "-m", "initial commit on main"); err != nil {
		tb.Fatalf("git commit: %v\n%s", err, out)
	}

	rootCommitBytes, err := g.Run("rev-parse", "HEAD")
	if err != nil {
		tb.Fatalf("rev-parse HEAD: %v", err)
	}
	rootCommit := strings.TrimSpace(rootCommitBytes)

	rootTreeBytes, err := g.Run("rev-parse", "HEAD^{tree}")
	if err != nil {
		tb.Fatalf("rev-parse tree: %v", err)
	}
	rootTree := strings.TrimSpace(rootTreeBytes)

	return dir, g, rootCommit, rootTree
}

func benchCommitTree(tb testing.TB, g *Git, tree, parent, msg string) string {
	tb.Helper()
	args := []string{"commit-tree", tree}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	args = append(args, "-m", msg)
	out, err := g.Run(args...)
	if err != nil {
		tb.Fatalf("commit-tree %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(out)
}

func benchUpdateRefs(tb testing.TB, dir string, commands []string) {
	tb.Helper()
	cmd := exec.Command("git", "-C", dir, "update-ref", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(commands, "\n") + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		tb.Fatalf("update-ref: %v\n%s", err, out)
	}
}

// setupFlatRepo creates count independent branches all branching directly off the root commit.
func setupFlatRepo(tb testing.TB, count int) (string, []string) {
	tb.Helper()
	dir, g, rootCommit, rootTree := setupBenchRepo(tb)

	commands := make([]string, 0, count)
	branches := make([]string, 0, count)
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("flat-%02d", i)
		cHash := benchCommitTree(tb, g, rootTree, rootCommit, fmt.Sprintf("commit flat %d", i))
		commands = append(commands, fmt.Sprintf("create refs/heads/%s %s", name, cHash))
		branches = append(branches, name)
	}
	benchUpdateRefs(tb, dir, commands)
	return dir, branches
}

// setupChainsRepo creates numChains chains of stacked branches, each of depth chainLen.
// Total branches = numChains * chainLen.
// Branches are returned in reverse order within each chain so Kahn's algorithm must order them base-first.
func setupChainsRepo(tb testing.TB, numChains, chainLen int) (string, []string) {
	tb.Helper()
	dir, g, rootCommit, rootTree := setupBenchRepo(tb)

	total := numChains * chainLen
	commands := make([]string, 0, total)
	chainBranches := make([][]string, numChains)

	for c := 0; c < numChains; c++ {
		parent := rootCommit
		chainBranches[c] = make([]string, chainLen)
		for d := 0; d < chainLen; d++ {
			name := fmt.Sprintf("chain-%d-%d", c, d)
			cHash := benchCommitTree(tb, g, rootTree, parent, fmt.Sprintf("commit chain %d depth %d", c, d))
			commands = append(commands, fmt.Sprintf("create refs/heads/%s %s", name, cHash))
			chainBranches[c][d] = name
			parent = cHash
		}
	}
	benchUpdateRefs(tb, dir, commands)

	// Interleave chains in reverse depth order: chain-0-9, chain-1-9, ... chain-0-0
	branches := make([]string, 0, total)
	for d := chainLen - 1; d >= 0; d-- {
		for c := 0; c < numChains; c++ {
			branches = append(branches, chainBranches[c][d])
		}
	}
	return dir, branches
}

// setupBinaryTreeRepo creates a binary tree topology of count branches.
// Node 0 is off rootCommit; for node i, children are 2*i+1 and 2*i+2.
// Branches are returned in reverse order (leaves first) so OrderByAncestry must sort root first.
func setupBinaryTreeRepo(tb testing.TB, count int) (string, []string) {
	tb.Helper()
	dir, g, rootCommit, rootTree := setupBenchRepo(tb)

	commands := make([]string, 0, count)
	commits := make([]string, count)
	names := make([]string, count)

	for i := 0; i < count; i++ {
		parent := rootCommit
		if i > 0 {
			parentIdx := (i - 1) / 2
			parent = commits[parentIdx]
		}
		names[i] = fmt.Sprintf("tree-node-%02d", i)
		commits[i] = benchCommitTree(tb, g, rootTree, parent, fmt.Sprintf("commit tree node %d", i))
		commands = append(commands, fmt.Sprintf("create refs/heads/%s %s", names[i], commits[i]))
	}
	benchUpdateRefs(tb, dir, commands)

	// Reverse order (leaves first)
	branches := make([]string, 0, count)
	for i := count - 1; i >= 0; i-- {
		branches = append(branches, names[i])
	}
	return dir, branches
}

// TestOrderByAncestryTopologies validates topological sorting across the 3 topologies.
func TestOrderByAncestryTopologies(t *testing.T) {
	t.Run("flat_100", func(t *testing.T) {
		repo, branches := setupFlatRepo(t, 100)
		got, err := OrderByAncestry(repo, branches)
		if err != nil {
			t.Fatalf("OrderByAncestry failed: %v", err)
		}
		if len(got) != 100 {
			t.Fatalf("expected 100 branches, got %d", len(got))
		}
		// Flat branches should retain deterministic relative input order
		for i, b := range branches {
			if got[i] != b {
				t.Fatalf("index %d: got %s, want %s", i, got[i], b)
			}
		}
	})

	t.Run("chains_of_10", func(t *testing.T) {
		repo, branches := setupChainsRepo(t, 10, 10)
		got, err := OrderByAncestry(repo, branches)
		if err != nil {
			t.Fatalf("OrderByAncestry failed: %v", err)
		}
		if len(got) != 100 {
			t.Fatalf("expected 100 branches, got %d", len(got))
		}

		pos := make(map[string]int, 100)
		for i, b := range got {
			pos[b] = i
		}

		// In each chain, lower depth must precede higher depth
		for c := 0; c < 10; c++ {
			for d := 0; d < 9; d++ {
				curr := fmt.Sprintf("chain-%d-%d", c, d)
				next := fmt.Sprintf("chain-%d-%d", c, d+1)
				if pos[curr] >= pos[next] {
					t.Errorf("chain %d: %s (pos %d) should precede %s (pos %d)", c, curr, pos[curr], next, pos[next])
				}
			}
		}
	})

	t.Run("binary_trees", func(t *testing.T) {
		repo, branches := setupBinaryTreeRepo(t, 100)
		got, err := OrderByAncestry(repo, branches)
		if err != nil {
			t.Fatalf("OrderByAncestry failed: %v", err)
		}
		if len(got) != 100 {
			t.Fatalf("expected 100 branches, got %d", len(got))
		}

		pos := make(map[string]int, 100)
		for i, b := range got {
			pos[b] = i
		}

		// In binary tree, parent must precede each child
		for i := 0; i < 50; i++ {
			parent := fmt.Sprintf("tree-node-%02d", i)
			left := 2*i + 1
			right := 2*i + 2
			if left < 100 {
				child := fmt.Sprintf("tree-node-%02d", left)
				if pos[parent] >= pos[child] {
					t.Errorf("%s (pos %d) should precede %s (pos %d)", parent, pos[parent], child, pos[child])
				}
			}
			if right < 100 {
				child := fmt.Sprintf("tree-node-%02d", right)
				if pos[parent] >= pos[child] {
					t.Errorf("%s (pos %d) should precede %s (pos %d)", parent, pos[parent], child, pos[child])
				}
			}
		}
	})
}

// BenchmarkOrderByAncestry benchmarks OrderByAncestry with 100 branches across varying stack depths:
// flat (depth 1), chains of 10 (10 chains of depth 10), and binary trees (depth 7).
func BenchmarkOrderByAncestry(b *testing.B) {
	b.Run("flat_100", func(b *testing.B) {
		repo, branches := setupFlatRepo(b, 100)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			got, err := OrderByAncestry(repo, branches)
			if err != nil {
				b.Fatalf("OrderByAncestry failed: %v", err)
			}
			if len(got) != 100 {
				b.Fatalf("expected 100 branches, got %d", len(got))
			}
		}
	})

	b.Run("chains_of_10", func(b *testing.B) {
		repo, branches := setupChainsRepo(b, 10, 10)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			got, err := OrderByAncestry(repo, branches)
			if err != nil {
				b.Fatalf("OrderByAncestry failed: %v", err)
			}
			if len(got) != 100 {
				b.Fatalf("expected 100 branches, got %d", len(got))
			}
		}
	})

	b.Run("binary_trees", func(b *testing.B) {
		repo, branches := setupBinaryTreeRepo(b, 100)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			got, err := OrderByAncestry(repo, branches)
			if err != nil {
				b.Fatalf("OrderByAncestry failed: %v", err)
			}
			if len(got) != 100 {
				b.Fatalf("expected 100 branches, got %d", len(got))
			}
		}
	})
}
