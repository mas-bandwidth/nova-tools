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

func TestDependencyCheckerResultsStoreAuthoritativeDone(t *testing.T) {
	resultsDir := t.TempDir()
	checker := NewGitAndResultsChecker("", "dev", resultsDir)

	// 1. Non-existent RESULT.md is rejected
	merged, reason := checker.IsDependencyMerged("card-prereq")
	if merged {
		t.Fatalf("expected non-existent RESULT.md to be rejected, got merged=true: %s", reason)
	}

	storeDir := filepath.Join(resultsDir, "card-prereq")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	resPath := filepath.Join(storeDir, "RESULT.md")

	// 2. Empty RESULT.md is rejected
	if err := os.WriteFile(resPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	merged, reason = checker.IsDependencyMerged("card-prereq")
	if merged {
		t.Fatalf("expected empty RESULT.md to be rejected, got merged=true: %s", reason)
	}

	// 3. Arbitrary in-progress text is rejected
	inProgressTexts := []string{
		"running build steps...",
		"IN_PROGRESS: shard 2 executing",
		"we are working on this card",
		"almost done with the task",
	}
	for _, text := range inProgressTexts {
		if err := os.WriteFile(resPath, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		merged, reason = checker.IsDependencyMerged("card-prereq")
		if merged {
			t.Fatalf("expected arbitrary in-progress text %q to be rejected, got merged=true: %s", text, reason)
		}
	}

	// 4. RESULT.md with FAILED is rejected (even if DONE is mentioned)
	failedTexts := []string{
		"RESULT card-prereq\nFAILED\n",
		"RESULT card-prereq\nFAILED: build timeout\n",
		"RESULT card-prereq\nFAILED: did not reach DONE step\n",
	}
	for _, text := range failedTexts {
		if err := os.WriteFile(resPath, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		merged, reason = checker.IsDependencyMerged("card-prereq")
		if merged {
			t.Fatalf("expected FAILED text %q to be rejected, got merged=true: %s", text, reason)
		}
	}

	// 5. RESULT.md with DONE is accepted
	doneTexts := []string{
		"RESULT card-prereq\nDONE\n",
		"RESULT card-prereq DONE\n",
		"DONE\n",
		"DONE sha=1234567\n",
	}
	for _, text := range doneTexts {
		if err := os.WriteFile(resPath, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		merged, reason = checker.IsDependencyMerged("card-prereq")
		if !merged {
			t.Fatalf("expected DONE text %q to be accepted, got merged=false: %s", text, reason)
		}
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

func TestDependencyCheckerRejectsMereMentionAndAcceptsMergeCommit(t *testing.T) {
	// Negative controls: commits merely mentioning #2484 or card-prereq must NOT satisfy the merge check
	mereMentions := []struct {
		subject string
		dep     string
	}{
		{"feat: some work mentioning #2484", "#2484"},
		{"fix: typo (fixes #2484)", "#2484"},
		{"docs: see #2484 for details", "#2484"},
		{"ref: update #2484", "#2484"},
		{"closes #2484", "#2484"},
		{"discussion on #2484", "#2484"},
		{"revert: pulse: update dependencies (#2484)", "#2484"},
		{"Revert \"pulse: update dependencies (#2484)\"", "#2484"},
		{"pulse: update dependencies (#24840)", "#2484"},
		{"feat: work mentioning card-prereq", "card-prereq"},
		{"fix: typo (fixes card-prereq)", "card-prereq"},
		{"card-other: depends on card-prereq", "card-prereq"},
		{"RESULT card-prereq-extra sha=123", "card-prereq"},
		{"card-prereq-extra: implement something", "card-prereq"},
	}

	for _, tc := range mereMentions {
		if IsCommitMergeOf(tc.subject, tc.dep) {
			t.Errorf("IsCommitMergeOf(%q, %q) = true, want false (mere mention should not satisfy merge check)", tc.subject, tc.dep)
		}
	}

	// Positive controls: actual merge commits, squash PR tags, batch approved PRs, and card landings
	realMerges := []struct {
		subject string
		dep     string
	}{
		{"Merge pull request #2484 from mas-bandwidth/emma/card-dependencies-2437", "#2484"},
		{"Merge PR #2484 from mas-bandwidth/patch", "#2484"},
		{"pulse: update dependencies (#2484)", "#2484"},
		{"pulse: update dependencies (#2484)", "2484"},
		{"pulse: update dependencies (#2484)", "mas-bandwidth/nova-tools#2484"},
		{"tools-batch: approved PRs (#2502 #2484 #2523)", "#2484"},
		{"tools-batch: approved PRs (#2502, #2484)", "#2484"},
		{"tools-20260922T223544Z: 2 approved PRs (#2689 #2695) — gated on hulk by land-lane (#2709)", "#2689"},
		{"tools-20260922T223544Z: 2 approved PRs (#2689 #2695) — gated on hulk by land-lane (#2709)", "#2709"},
		{"merge: friend name is case-insensitive (#2615, #2631 follow-up)", "#2615"},
		{"merge: friend name is case-insensitive (#2615, #2631 follow-up)", "#2631"},
		{"RESULT card-prereq sha=abc1234 — green", "card-prereq"},
		{"RESULT card-prereq: all green", "card-prereq"},
		{"RESULT card-prereq", "card-prereq"},
		{"RESULT card-prereq", "prereq"},
		{"card-prereq: initial implementation", "card-prereq"},
		{"[card-prereq] initial implementation", "card-prereq"},
	}

	for _, tc := range realMerges {
		if !IsCommitMergeOf(tc.subject, tc.dep) {
			t.Errorf("IsCommitMergeOf(%q, %q) = false, want true (real merge commit should satisfy merge check)", tc.subject, tc.dep)
		}
	}

	// End-to-end GitAndResultsChecker test:
	// A repo log containing only mere mentions should report dependency unmerged.
	checkerMentionOnly := &GitAndResultsChecker{
		Repo:       ".",
		BaseBranch: "dev",
		RunGit: func(dir string, args ...string) (string, error) {
			sub := strings.Join(args, " ")
			if strings.Contains(sub, "log dev") {
				return "feat: some work mentioning #2484\nfix: typo (fixes #2484)\n", nil
			}
			return "", os.ErrNotExist
		},
	}
	merged, reason := checkerMentionOnly.IsDependencyMerged("#2484")
	if merged {
		t.Fatalf("expected #2484 to be unsatisfied with only mere mentions, but got merged: %s", reason)
	}

	// A repo log containing an actual squash merge should report dependency merged.
	checkerMerged := &GitAndResultsChecker{
		Repo:       ".",
		BaseBranch: "dev",
		RunGit: func(dir string, args ...string) (string, error) {
			sub := strings.Join(args, " ")
			if strings.Contains(sub, "log dev") {
				return "pulse: update dependencies (#2484)\n", nil
			}
			return "", os.ErrNotExist
		},
	}
	merged, reason = checkerMerged.IsDependencyMerged("#2484")
	if !merged {
		t.Fatalf("expected #2484 to be satisfied with squash merge commit, but got unmerged: %s", reason)
	}
}
