package ci

import (
	"path/filepath"
	"strings"
	"testing"
)

// ci_windows_pr_test.go holds the Windows-on-a-pull-request leg shut.
//
// The cost of not having it: Windows ran only in the merge group
// (test-hosted-merge) and on push, so a Windows-only failure — an assertion on
// the execute bit, a backslash where the test expected "a/b", a fake binary
// written without the .exe suffix — was found after a PR was enqueued, where a
// red shard drops the whole group and restarts every PR behind it. One PR's
// small mistake became every PR's delay.
//
// The leg that fixes that is only worth its hosted minutes if it stays cheap, so
// the shape is the contract and this test is the shape: ONE job (no matrix), on
// windows-latest, over the packages .github/scripts/select-packages.sh selects —
// the same script the self-hosted shards call, so there is one answer to "what
// does this change test" — skipped when the PR moves no Go file, guarded against
// fork code, and aggregated by ci-ok. Read as text, like the rest of this
// package: go.mod carries no YAML library.
func TestPullRequestsGetAWindowsLeg(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	job := jobBody(src, "test-windows-pr")
	if job == "" {
		t.Fatal("no test-windows-pr job in ci.yml: a pull request gets no Windows leg, so a Windows-only break is found in the merge group, where it drops the whole group")
	}

	if !strings.Contains(job, "runs-on: windows-latest") {
		t.Error("test-windows-pr does not run on windows-latest; there is no self-hosted Windows runner, so the leg has nowhere else to go")
	}
	if strings.Contains(job, "strategy:") || strings.Contains(job, "matrix:") {
		t.Error("test-windows-pr carries a matrix; these are billed hosted minutes at the Windows rate and the leg is deliberately ONE job over the changed packages")
	}
	if !strings.Contains(job, "github.event_name == 'pull_request'") {
		t.Error("test-windows-pr is not guarded to pull_request; the merge group and the push already run Windows, and paying twice is the one thing this leg must not do")
	}
	if !strings.Contains(job, "github.event.pull_request.head.repo.full_name == github.repository") {
		t.Error("test-windows-pr has no head-repo guard; a fork PR would run untrusted code on hosted compute")
	}
	if !strings.Contains(job, ".github/scripts/select-packages.sh") {
		t.Error("test-windows-pr does not select its packages with .github/scripts/select-packages.sh; a second answer to `what does this change test` is a second thing to keep in step with the shards")
	}
	if !strings.Contains(job, "'**/*.go'") {
		t.Error("test-windows-pr has no path filter on **/*.go; a docs or lisp PR would pay for a Windows runner that has nothing to test")
	}

	// The aggregate must see it: a leg no required check reads is a leg whose
	// red nobody has to fix.
	i := strings.Index(src, "\n  ci-ok:")
	if i < 0 {
		t.Fatal("no ci-ok job in ci.yml")
	}
	ciok := src[i:]
	if !strings.Contains(ciok, "test-windows-pr") {
		t.Error("ci-ok does not aggregate test-windows-pr; the only required check would be green over a red Windows leg")
	}
	if !strings.Contains(ciok, "needs.test-windows-pr.result") {
		t.Error("ci-ok's verdict steps do not read needs.test-windows-pr.result; listing a job in `needs` alone does not make its red a red verdict")
	}
}
