package ci

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const workspaceCleanupStepName = "remove stale build dirs from the shared runner"

var (
	// An empty GITHUB_WORKSPACE is still worth refusing.
	emptyWorkspaceRefusalRe = regexp.MustCompile(`-n\s+"?\$\{?GITHUB_WORKSPACE\}?"?\s*\]\s*\|\|\s*exit\s+1`)
	// A workspace directory that does not exist yet is the normal first-run
	// state: clean nothing and continue.
	absentWorkspaceContinueRe = regexp.MustCompile(`-d\s+"?\$\{?GITHUB_WORKSPACE\}?"?\s*\]\s*\|\|\s*exit\s+0`)
	// THE BELT. A workspace that exists but carries no `.git` is not a
	// checkout of this repository, and `find … -exec rm -rf` over it would be
	// emptying a directory nobody has identified. Continue instead: there is
	// nothing of ours in there to clean, and actions/checkout empties a
	// non-repository workspace itself before it clones.
	nonRepoWorkspaceBeltRe = regexp.MustCompile(`-d\s+"?\$\{?GITHUB_WORKSPACE\}?"?/\.git"?\s*\]\s*\|\|\s*exit\s+0`)
	// A `.git` test that exits NON-zero is the #1751 defect itself: the step
	// runs before checkout, so an absent `.git` is the normal first-run state.
	gitRefusalRe = regexp.MustCompile(`\.git[^\n]*\|\|\s*exit\s+[1-9]`)
	// The destructive line the two guards above stand in front of.
	sweepRe = regexp.MustCompile(`find\s+"?\$\{?GITHUB_WORKSPACE\}?"?[^\n]*rm\s+-rf`)
)

// ciworkspace_class_test.go closes #1751 and carries its belt. The `remove
// stale build dirs from the shared runner` step runs BEFORE actions/checkout,
// and its original first line demanded a `.git` under GITHUB_WORKSPACE, so on a
// runner whose workspace did not exist yet the step exited 1 and the job was red
// before the repository was read. The repair may not swing the other way: the
// step ends in `find "${GITHUB_WORKSPACE}" … -exec rm -rf`, so it must refuse an
// empty variable, continue over a workspace that does not exist, AND continue
// over a workspace that is not a checkout of this repository. The step is read
// as TEXT, like the rest of internal/ci, because go.mod carries no YAML library.
// It appears once per self-hosted job, so the test walks EVERY occurrence:
// asserting only the first would pass a tree where five are fixed.
func TestWorkspaceCleanupDoesNotFailBeforeCheckout(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	lines, bodies := workspaceCleanupStepBodies(src)
	if len(bodies) == 0 {
		t.Fatalf("no `%s` step in ci.yml; #1751's precheck has moved and this test is looking in the wrong place", workspaceCleanupStepName)
	}
	for i, body := range bodies {
		where := fmt.Sprintf("occurrence %d of %d (ci.yml line %d)", i+1, len(bodies), lines[i])
		if gitRefusalRe.MatchString(body) {
			t.Errorf("%s: the cleanup step still refuses a workspace with no .git; it runs before checkout, so a runner whose workspace does not exist yet goes red before a line of the repository is read (#1751)", where)
		}
		if !emptyWorkspaceRefusalRe.MatchString(body) {
			t.Errorf("%s: the cleanup step no longer refuses an empty GITHUB_WORKSPACE (`[ -n \"${GITHUB_WORKSPACE}\" ] || exit 1`); a missing workspace is still worth refusing (#1751)", where)
		}
		if !absentWorkspaceContinueRe.MatchString(body) {
			t.Errorf("%s: the cleanup step has no `[ -d \"${GITHUB_WORKSPACE}\" ] || exit 0` guard; a missing workspace directory is the normal first-run state and must continue (#1751)", where)
		}
		if sweepRe.MatchString(body) && !nonRepoWorkspaceBeltRe.MatchString(body) {
			t.Errorf("%s: the cleanup step runs `find \"${GITHUB_WORKSPACE}\" … -exec rm -rf` with no `[ -d \"${GITHUB_WORKSPACE}/.git\" ] || exit 0` belt in front of it; a workspace that exists but is not a checkout of this repository must be left alone rather than emptied (#1751)", where)
		}
	}
}

// workspaceCleanupStepBodies returns the 1-based line number and body of every
// occurrence of the cleanup step, so a fix in five of six is caught by name.
func workspaceCleanupStepBodies(src string) ([]int, []string) {
	lines := strings.Split(src, "\n")
	var nums []int
	var bodies []string
	for i := 0; i < len(lines); i++ {
		j := strings.Index(lines[i], "- name: "+workspaceCleanupStepName)
		if j < 0 {
			continue
		}
		indent := lines[i][:j]
		end := len(lines)
		for k := i + 1; k < len(lines); k++ {
			trimmed := strings.TrimLeft(lines[k], " ")
			if strings.HasPrefix(trimmed, "- name:") && len(lines[k])-len(trimmed) == len(indent) {
				end = k
				break
			}
		}
		nums = append(nums, i+1)
		bodies = append(bodies, strings.Join(lines[i:end], "\n"))
		i = end - 1
	}
	return nums, bodies
}
