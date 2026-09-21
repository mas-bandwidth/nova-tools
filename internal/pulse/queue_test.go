package pulse

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeTestCard(t *testing.T, dir, filename, label string, priority int, deps ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("RESULT " + label + "\n")
	if priority != 0 {
		b.WriteString("PRIORITY: " + string(rune('0'+priority)) + "\n")
	}
	if len(deps) > 0 {
		b.WriteString("depends-on: " + strings.Join(deps, ", ") + "\n")
	}
	p := filepath.Join(dir, filename)
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestQueueTopologicalSort(t *testing.T) {
	dir := t.TempDir()

	// Chain: card-1 -> card-2 -> card-3
	// card-3 depends on card-2, card-2 depends on card-1, card-1 has no deps.
	c3 := writeTestCard(t, dir, "card-003.md", "card-3", 0, "card-2")
	c1 := writeTestCard(t, dir, "card-001.md", "card-1", 0)
	c2 := writeTestCard(t, dir, "card-002.md", "card-2", 0, "card-1")

	cards := []string{c3, c2, c1}
	sorted := SortQueueCards(cards)

	want := []string{c1, c2, c3}
	if !reflect.DeepEqual(sorted, want) {
		t.Fatalf("SortQueueCards got %v, want %v", sorted, want)
	}
}

func TestQueuePriorityOrdering(t *testing.T) {
	dir := t.TempDir()

	// Three independent cards with different priorities
	c1 := writeTestCard(t, dir, "card-001.md", "card-1", 1)
	c2 := writeTestCard(t, dir, "card-002.md", "card-2", 9)
	c3 := writeTestCard(t, dir, "card-003.md", "card-3", 5)

	sorted := SortQueueCards([]string{c1, c2, c3})
	want := []string{c2, c3, c1} // Priority 9 -> 5 -> 1
	if !reflect.DeepEqual(sorted, want) {
		t.Fatalf("SortQueueCards priority got %v, want %v", sorted, want)
	}
}

func TestQueueTopologicalConstrainedPriority(t *testing.T) {
	dir := t.TempDir()

	// card-high has priority 9, but depends on card-prereq (priority 1).
	// card-indep has priority 5, no deps.
	// card-prereq must come before card-high even though card-high has higher priority!
	cHigh := writeTestCard(t, dir, "card-002.md", "card-high", 9, "card-prereq")
	cIndep := writeTestCard(t, dir, "card-003.md", "card-indep", 5)
	cPrereq := writeTestCard(t, dir, "card-001.md", "card-prereq", 1)

	sorted := SortQueueCards([]string{cHigh, cIndep, cPrereq})

	// card-indep (5) vs card-prereq (1): both have inDegree 0.
	// card-indep (5) is admitted first among inDegree 0.
	// card-prereq (1) is admitted second.
	// card-high (9) is unblocked and admitted third.
	want := []string{cIndep, cPrereq, cHigh}
	if !reflect.DeepEqual(sorted, want) {
		t.Fatalf("Topological constrained priority got %v, want %v", sorted, want)
	}
}

func TestQueueCycleResilience(t *testing.T) {
	dir := t.TempDir()

	// Circular dependency: A -> B -> A
	cA := writeTestCard(t, dir, "card-a.md", "card-a", 2, "card-b")
	cB := writeTestCard(t, dir, "card-b.md", "card-b", 1, "card-a")

	sorted := SortQueueCards([]string{cB, cA})
	// In cycle, all cards are returned safely without crashing, ordered by priority
	if len(sorted) != 2 {
		t.Fatalf("expected 2 cards, got %d", len(sorted))
	}
	if sorted[0] != cA || sorted[1] != cB {
		t.Errorf("expected [cA, cB], got %v", sorted)
	}
}

func TestDependencyCheckerResultsStore(t *testing.T) {
	resultsDir := t.TempDir()

	checker := NewGitAndResultsChecker("", "dev", resultsDir)

	// 1. Dependency not yet in store
	merged, reason := checker.IsDependencyMerged("card-prereq")
	if merged {
		t.Errorf("expected not merged, got true: %s", reason)
	}

	// 2. Dependency lands in results store
	storeDir := filepath.Join(resultsDir, "card-prereq")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "RESULT.md"), []byte("RESULT card-prereq\nDONE\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	merged, reason = checker.IsDependencyMerged("card-prereq")
	if !merged {
		t.Errorf("expected merged after RESULT.md, got false: %s", reason)
	}

	// Also matches by bare name or card- prefix
	merged, _ = checker.IsDependencyMerged("prereq")
	if !merged {
		t.Errorf("expected merged by bare name, got false")
	}
}

func TestDependencyCheckerGitMock(t *testing.T) {
	checker := &GitAndResultsChecker{
		Repo:       ".",
		BaseBranch: "dev",
		RunGit: func(dir string, args ...string) (string, error) {
			sub := strings.Join(args, " ")
			switch {
			case strings.Contains(sub, "cat-file -e dev:internal/pulse/fill.go"):
				return "", nil
			case strings.Contains(sub, "cat-file -e dev:missing/file.go"):
				return "", os.ErrNotExist
			case strings.Contains(sub, "merge-base --is-ancestor feat-merged dev"):
				return "", nil
			case strings.Contains(sub, "merge-base --is-ancestor feat-unmerged dev"):
				return "", os.ErrNotExist
			}
			return "", os.ErrNotExist
		},
	}

	// File on dev@tip
	merged, reason := checker.IsDependencyMerged("mas-bandwidth/nova-tools:internal/pulse/fill.go")
	if !merged {
		t.Errorf("expected file path to be satisfied on dev@tip, got false: %s", reason)
	}

	// Missing file on dev@tip
	merged, reason = checker.IsDependencyMerged("mas-bandwidth/nova-tools:missing/file.go")
	if merged {
		t.Errorf("expected missing file to be unsatisfied, got true: %s", reason)
	}

	// Merged ancestor branch
	merged, reason = checker.IsDependencyMerged("feat-merged")
	if !merged {
		t.Errorf("expected feat-merged to be satisfied, got false: %s", reason)
	}

	// Unmerged branch
	merged, reason = checker.IsDependencyMerged("feat-unmerged")
	if merged {
		t.Errorf("expected feat-unmerged to be unsatisfied, got true: %s", reason)
	}
}
