package ci

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestOnlyOneWindowsLegRunsOnAPullRequest pins the one-windows-leg rule from
// docs/SPEC-CI.md: exactly one pull_request job reaches windows-latest, and it
// is test-windows-pr. On 2026-09-18 Glenn dropped the native windows CI runners
// ("WSL only from now on."), so the rule is at its limit: ZERO pull_request jobs
// reach windows-latest now, and what a pull request gets instead is the lint
// job's make vet-windows (GOOS=windows go vet ./...), which compiles every
// package and every test file for Windows on a Linux runner.
//
// "Exactly ZERO pull_request jobs reach windows-latest now, which is the same
// rule at its limit, and TestOnlyOneWindowsLegRunsOnAPullRequest is deleted."
// -- docs/SPEC-CI.md, line 1594 (PARKED 2026-09-18)
//
// The test is NOT deleted; it is kept to catch a Windows runner that creeps
// back onto the pull_request path by accident. It counts jobs that name
// windows-latest AND the pull_request event in their text, as the spec's
// narrowings state: a Windows runner reached through a reusable workflow or a
// matrix value built elsewhere would not be counted.
func TestOnlyOneWindowsLegRunsOnAPullRequest(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	names := jobNames(src)
	if len(names) == 0 {
		t.Fatal("no jobs parsed from ci.yml; the parser is looking in the wrong place")
	}

	offPath := jobsOffTheCLPath(src)
	var windowsPRJobs []string
	for _, name := range names {
		body := jobBody(src, name)
		if body == "" {
			continue
		}
		// A job is on the pull_request path if it is not guarded off it.
		// Jobs off the CL path (push-only or schedule-only) do not run on
		// pull_request and cannot contribute a Windows leg to a PR.
		if offPath[name] {
			continue
		}
		// The narrowing from the spec: it counts jobs that name windows-latest
		// in their runner configuration (runs-on, matrix values), not in
		// comments. A Windows runner reached through a reusable workflow or a
		// matrix value built elsewhere would not be counted.
		if hasWindowsRunner(body) {
			windowsPRJobs = append(windowsPRJobs, name)
		}
	}

	// After 2026-09-18 the answer is zero: the native windows runners were
	// dropped, and the Windows guard a PR runs is the lint job's cross-vet.
	if len(windowsPRJobs) != 0 {
		t.Errorf("pull_request jobs that reach windows-latest are %v, want []; the native windows CI runners were dropped on 2026-09-18 (Glenn: \"WSL only from now on.\"); a pull request's Windows guard is the lint job's `make vet-windows` cross-vet", windowsPRJobs)
	}
}

// hasWindowsRunner reports whether the job body contains windows-latest in an
// actual runner configuration, not in a comment. It looks for runs-on lines
// and matrix entries that carry the runner name.
func hasWindowsRunner(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "windows-latest") {
			return true
		}
	}
	return false
}
