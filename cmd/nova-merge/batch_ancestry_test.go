package main

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// stackedBatchRepo creates a bare fixture repository with:
// - dev branch as the base
// - pr1: parent commit branching off dev
// - pr2: child commit stacked on pr1
// - pr3: grandchild commit stacked on pr2
// - pr4: independent commit branching off dev
func stackedBatchRepo(t *testing.T) *lab {
	t.Helper()
	l := newLab(t)
	l.urlFor = func(string) string { return fileURL(l.remote) }
	l.git(l.work, "checkout", "-q", "-B", "dev", "origin/main")
	l.write("go.mod", "module example.com/batch\n\ngo 1.21\n")
	l.write("base/base.go", "package base\n\nfunc Base() int { return 1 }\n")
	dev := l.commit("dev base")
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/dev")

	// pr1: parent
	l.git(l.work, "checkout", "-q", "-B", "pr1", dev)
	l.write("pkg/p1/p1.go", "package p1\n\nfunc P1() int { return 1 }\n")
	one := l.commit("pr 1: parent")

	// pr2: child stacked on pr1
	l.git(l.work, "checkout", "-q", "-B", "pr2", "pr1")
	l.write("pkg/p2/p2.go", "package p2\n\nimport \"example.com/batch/pkg/p1\"\n\nfunc P2() int { return p1.P1() + 1 }\n")
	two := l.commit("pr 2: child stacked on pr1")

	// pr3: grandchild stacked on pr2
	l.git(l.work, "checkout", "-q", "-B", "pr3", "pr2")
	l.write("pkg/p3/p3.go", "package p3\n\nimport \"example.com/batch/pkg/p2\"\n\nfunc P3() int { return p2.P2() + 1 }\n")
	three := l.commit("pr 3: grandchild stacked on pr2")

	// pr4: independent branch off dev
	l.git(l.work, "checkout", "-q", "-B", "pr4", dev)
	l.write("pkg/p4/p4.go", "package p4\n\nfunc P4() int { return 4 }\n")
	four := l.commit("pr 4: independent")

	l.heads = map[int]string{1: one, 2: two, 3: three, 4: four}
	for n, sha := range l.heads {
		l.git(l.work, "push", "-q", "origin", sha+":refs/pull/"+strconv.Itoa(n)+"/head")
		l.host.PRs[n] = merge.PR{Number: n, HeadOID: sha, Base: "dev"}
		l.host.SetCheckRuns(sha, merge.CheckDetail{Name: "ci-ok", Conclusion: "success", SHA: sha})
	}
	l.git(l.work, "checkout", "-q", "main")
	return l
}

// TestAncestryPlanPRsUnit tests planPRsByAncestry directly on all permutations of stacked PRs.
func TestAncestryPlanPRsUnit(t *testing.T) {
	t.Parallel()
	l := stackedBatchRepo(t)

	// Fetch all heads into a local clone so ancestry can be inspected.
	cloneDir := filepath.Join(l.dir, "test-ancestry-clone")
	l.git(l.dir, "clone", "--quiet", fileURL(l.remote), cloneDir)
	g := merge.NewGit(cloneDir, time.Minute, l.runner)

	want := []int{1, 2, 3}
	permutations := [][]int{
		{3, 2, 1},
		{2, 3, 1},
		{3, 1, 2},
		{2, 1, 3},
		{1, 3, 2},
		{1, 2, 3},
	}

	for _, input := range permutations {
		got, err := planPRsByAncestry(g, cloneDir, input)
		if err != nil {
			t.Fatalf("planPRsByAncestry(%v) error: %v", input, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("planPRsByAncestry(%v) = %v, want %v", input, got, want)
		}
	}
}

// TestAncestryPlanBranchesUnit tests planBranchesByAncestry directly on stacked branches.
func TestAncestryPlanBranchesUnit(t *testing.T) {
	t.Parallel()
	l := stackedBatchRepo(t)

	cloneDir := filepath.Join(l.dir, "test-ancestry-branches-clone")
	l.git(l.dir, "clone", "--quiet", fileURL(l.remote), cloneDir)
	g := merge.NewGit(cloneDir, time.Minute, l.runner)

	// Fetch the PR branches as local branches
	for _, n := range []int{1, 2, 3} {
		l.git(cloneDir, "fetch", "--quiet", "origin", fmt.Sprintf("pull/%d/head:branch-%d", n, n))
	}

	got, err := planBranchesByAncestry(g, cloneDir, []string{"branch-3", "branch-1", "branch-2"})
	if err != nil {
		t.Fatalf("planBranchesByAncestry error: %v", err)
	}
	want := []string{"branch-1", "branch-2", "branch-3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("planBranchesByAncestry = %v, want %v", got, want)
	}
}

// TestAncestryIndependentPRsPreserveOrder tests that independent PRs retain their input order.
func TestAncestryIndependentPRsPreserveOrder(t *testing.T) {
	t.Parallel()
	l := stackedBatchRepo(t)

	cloneDir := filepath.Join(l.dir, "test-ancestry-indep-clone")
	l.git(l.dir, "clone", "--quiet", fileURL(l.remote), cloneDir)
	g := merge.NewGit(cloneDir, time.Minute, l.runner)

	// PR 1 and PR 4 are independent branches off dev.
	got14, err := planPRsByAncestry(g, cloneDir, []int{1, 4})
	if err != nil {
		t.Fatalf("planPRsByAncestry(1, 4) error: %v", err)
	}
	if !reflect.DeepEqual(got14, []int{1, 4}) {
		t.Errorf("got %v, want [1, 4]", got14)
	}

	got41, err := planPRsByAncestry(g, cloneDir, []int{4, 1})
	if err != nil {
		t.Fatalf("planPRsByAncestry(4, 1) error: %v", err)
	}
	if !reflect.DeepEqual(got41, []int{4, 1}) {
		t.Errorf("got %v, want [4, 1]", got41)
	}
}

// TestAncestryBatchLandsParentBeforeChild proves that stacked PRs supplied in reverse order
// (child before parent: --pr 2,1) are planned and landed parent-before-child (members=1,2).
func TestAncestryBatchLandsParentBeforeChild(t *testing.T) {
	t.Parallel()
	l := stackedBatchRepo(t)
	root := filepath.Join(l.dir, "batch")

	// Pass child PR 2 before parent PR 1:
	exit, stdout, stderr := l.run("batch", "--name", "integration-stack-21", "--pr", "2,1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m")

	if exit != 0 {
		t.Fatalf("batch exit = %d, want 0\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}

	// 1. In stdout, members must list parent (1) before child (2):
	contains(t, stdout, "BATCH OK name=integration-stack-21")
	contains(t, stdout, "members=1,2")
	contains(t, stdout, "dropped=none")

	// 2. In stderr, BATCH MERGED #1 must appear before BATCH MERGED #2:
	merged1Idx := strings.Index(stderr, "BATCH MERGED #1")
	merged2Idx := strings.Index(stderr, "BATCH MERGED #2")
	if merged1Idx == -1 {
		t.Fatalf("missing 'BATCH MERGED #1' in stderr:\n%s", stderr)
	}
	if merged2Idx == -1 {
		t.Fatalf("missing 'BATCH MERGED #2' in stderr:\n%s", stderr)
	}
	if merged1Idx >= merged2Idx {
		t.Errorf("parent PR 1 was not merged before child PR 2: merged1Idx=%d, merged2Idx=%d", merged1Idx, merged2Idx)
	}

	// 3. Inspect git history of the created integration branch: parent commit must be ancestor of child commit.
	clone := filepath.Join(root, "integration-stack-21", "repo")
	logOut := l.git(clone, "log", "--oneline", "refs/heads/rowan/integration-stack-21")
	idxMerge1 := strings.Index(logOut, "merge pull request #1 into integration-stack-21")
	idxMerge2 := strings.Index(logOut, "merge pull request #2 into integration-stack-21")
	if idxMerge1 == -1 || idxMerge2 == -1 {
		t.Fatalf("unexpected git log on integration branch:\n%s", logOut)
	}
	// In git log (newest first), merge #2 (child) must appear before merge #1 (parent).
	if idxMerge2 >= idxMerge1 {
		t.Errorf("in git log (newest first), child merge should appear before parent merge:\n%s", logOut)
	}
}

// TestAncestryBatchThreeStackedPRsInReverseOrder proves three stacked PRs passed in reverse
// order (--pr 3,2,1) land in topological parent-to-child order (members=1,2,3).
func TestAncestryBatchThreeStackedPRsInReverseOrder(t *testing.T) {
	t.Parallel()
	l := stackedBatchRepo(t)
	root := filepath.Join(l.dir, "batch")

	exit, stdout, stderr := l.run("batch", "--name", "integration-stack-321", "--pr", "3,2,1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m")

	if exit != 0 {
		t.Fatalf("batch exit = %d, want 0\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}

	contains(t, stdout, "BATCH OK name=integration-stack-321")
	contains(t, stdout, "members=1,2,3")
	contains(t, stdout, "dropped=none")

	merged1Idx := strings.Index(stderr, "BATCH MERGED #1")
	merged2Idx := strings.Index(stderr, "BATCH MERGED #2")
	merged3Idx := strings.Index(stderr, "BATCH MERGED #3")

	if merged1Idx >= merged2Idx || merged2Idx >= merged3Idx {
		t.Errorf("merges out of ancestry order: 1=%d, 2=%d, 3=%d\nstderr:\n%s",
			merged1Idx, merged2Idx, merged3Idx, stderr)
	}
}

// TestAncestryBatchMixedStackedAndIndependent proves interleaved stacked and independent PRs
// land parent-before-child while preserving independent PR priority.
func TestAncestryBatchMixedStackedAndIndependent(t *testing.T) {
	t.Parallel()
	l := stackedBatchRepo(t)
	root := filepath.Join(l.dir, "batch")

	// Pass independent PR 4 first, then child 3, child 2, parent 1:
	exit, stdout, stderr := l.run("batch", "--name", "integration-mixed", "--pr", "4,3,2,1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m")

	if exit != 0 {
		t.Fatalf("batch exit = %d, want 0\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}

	// 4 came first and is independent of 1, 2, 3; 1 is ancestor of 2; 2 is ancestor of 3:
	contains(t, stdout, "BATCH OK name=integration-mixed")
	contains(t, stdout, "members=4,1,2,3")
	contains(t, stdout, "dropped=none")

	merged1Idx := strings.Index(stderr, "BATCH MERGED #1")
	merged2Idx := strings.Index(stderr, "BATCH MERGED #2")
	merged3Idx := strings.Index(stderr, "BATCH MERGED #3")

	if merged1Idx >= merged2Idx || merged2Idx >= merged3Idx {
		t.Errorf("stacked members out of ancestry order: 1=%d, 2=%d, 3=%d", merged1Idx, merged2Idx, merged3Idx)
	}
}

// TestAncestryBatchNonexistentPRRefuses proves a nonexistent PR causes an immediate refusal at exit 2.
func TestAncestryBatchNonexistentPRRefuses(t *testing.T) {
	t.Parallel()
	l := stackedBatchRepo(t)
	root := filepath.Join(l.dir, "batch")

	exit, _, stderr := l.run("batch", "--name", "integration-nonexistent", "--pr", "1,999",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m")

	if exit != 2 {
		t.Fatalf("batch exit = %d, want 2 for nonexistent PR", exit)
	}
	contains(t, stderr, "BATCH REFUSED")
	contains(t, stderr, "could not fetch pull/999/head")
}
