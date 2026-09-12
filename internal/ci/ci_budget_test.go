package ci

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// ci_budget_test.go is the two-minute law, read off the workflow files as text
// rather than from any one job's log. The maintainer's rule is "CI checks per
// every CL, one minute ideal, two minutes maximum", applied to the COMPLETE
// required path, not to each job alone — so the fast tier is a design in
// ci.yml, and this test pins that design so a future edit that quietly pushes a
// check back over the budget is a red run instead of a slow morning.
//
// It reads the files as text on purpose: the repository has no YAML library in
// go.mod (standard library only), so the checks are shape checks over the
// lines, exactly as strict as the shape they assert and nothing more.
//
// Three invariants:
//   (a) every job in ci.yml declares timeout-minutes, and none exceeds 2 — the
//       aggregate ci-ok may be 1 — so the CL tier cannot silently exceed the
//       budget;
//   (b) every job name that left ci.yml relative to origin/main is present in
//       the certification workflow by the same name, so the split deleted
//       nothing;
//   (c) every `uses:` in both files is owner/action@40-hex-sha, so an action
//       cannot drift under a mutable tag.

// jobKeyRe matches a job name: a key at exactly two spaces under `jobs:`.
var jobKeyRe = regexp.MustCompile(`^  ([a-zA-Z0-9_-]+):$`)

// timeoutRe matches a timeout-minutes line at four spaces.
var timeoutRe = regexp.MustCompile(`^    timeout-minutes:\s*(\d+)$`)

// usesRe matches an action reference pinned by its 40-hex commit SHA.
var usesRe = regexp.MustCompile(`uses:\s*([^/\s]+/[^@\s]+)@([0-9a-fA-F]{40})`)

// usesDirectiveRe matches a line that IS a `uses:` step key — either a list
// item (`- uses:`) or a map key (`uses:`), at any indentation. It deliberately
// will not match a string that merely CONTAINS "uses:" in the middle of a word
// (the smoke gate lists an "unreadable deny-list refuses:" step, whose
// "refuses:" is not an action reference).
var usesDirectiveRe = regexp.MustCompile(`^\s*(-\s*)?uses:\s*\S`)

func TestCLTierJobsStayWithinTheBudget(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	names := jobNames(src)
	if len(names) == 0 {
		t.Fatal("no jobs parsed from ci.yml; the parser is looking in the wrong place")
	}
	timeouts := jobTimeouts(src)
	for _, name := range names {
		mins, ok := timeouts[name]
		if !ok {
			t.Errorf("CL-tier job %q has no timeout-minutes; the budget cannot be enforced on a job that does not declare one", name)
			continue
		}
		// The aggregate ci-ok is allowed to be 1; every other CL-tier job must be 2 or less.
		if name == "ci-ok" {
			if mins > 1 {
				t.Errorf("the ci-ok aggregate has timeout-minutes %d, want <= 1", mins)
			}
			continue
		}
		if mins > 2 {
			t.Errorf("CL-tier job %q has timeout-minutes %d, want <= 2 (the maintainer's two-minute maximum)", name, mins)
		}
	}
}

func TestJobsThatLeftCIAreStillInCertification(t *testing.T) {
	root := repoRoot(t)
	newCI := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	cert := readFile(t, filepath.Join(root, ".github", "workflows", "certification.yml"))
	oldCI := originMainCI(t, root)

	newNames := toSet(jobNames(newCI))
	certNames := toSet(jobNames(cert))
	if len(certNames) == 0 {
		t.Fatal("no jobs parsed from certification.yml; the parser is looking in the wrong place")
	}
	for _, name := range jobNames(oldCI) {
		if newNames[name] {
			continue // still in ci.yml, so it did not leave
		}
		if !certNames[name] {
			t.Errorf("job %q left ci.yml but is not present in certification.yml; the split must delete nothing", name)
		}
	}
}

func TestEveryActionIsPinnedBySHA(t *testing.T) {
	root := repoRoot(t)
	for _, file := range []string{".github/workflows/ci.yml", ".github/workflows/certification.yml"} {
		src := readFile(t, filepath.Join(root, file))
		lines := strings.Split(src, "\n")
		for i, line := range lines {
			if !usesDirectiveRe.MatchString(line) {
				continue
			}
			if usesRe.FindStringSubmatch(line) == nil {
				t.Errorf("%s:%d: uses: is not owner/action@40-hex-sha: %q", file, i+1, strings.TrimSpace(line))
			}
		}
	}
}

func jobNames(src string) []string {
	var names []string
	inJobs := false
	for _, line := range strings.Split(src, "\n") {
		if !strings.HasPrefix(line, " ") && strings.TrimSpace(line) == "jobs:" {
			inJobs = true
			continue
		}
		if !inJobs {
			continue
		}
		if line == "" {
			continue
		}
		// A top-level key (column 0) ends the jobs section.
		if !strings.HasPrefix(line, " ") {
			break
		}
		if m := jobKeyRe.FindStringSubmatch(line); m != nil {
			names = append(names, m[1])
		}
	}
	return names
}

func jobTimeouts(src string) map[string]int {
	out := make(map[string]int)
	cur := ""
	for _, line := range strings.Split(src, "\n") {
		if m := jobKeyRe.FindStringSubmatch(line); m != nil {
			cur = m[1]
			continue
		}
		if cur == "" {
			continue
		}
		if m := timeoutRe.FindStringSubmatch(line); m != nil {
			n, err := strconv.Atoi(m[1])
			if err == nil {
				out[cur] = n
			}
		}
	}
	return out
}

func toSet(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

// originMainCI returns the ci.yml at origin/main, so the comparison in
// TestJobsThatLeftCIAreStillInCertification has a baseline that is not this
// branch. A shallow CI checkout may not carry origin/main, so it is fetched
// first when the ref is missing. This is not a skip: a comparison with no
// baseline must fail, not pass.
func originMainCI(t *testing.T, root string) string {
	t.Helper()
	show := exec.Command("git", "show", "origin/main:.github/workflows/ci.yml")
	show.Dir = root
	if out, err := show.Output(); err == nil {
		return string(out)
	}
	fetch := exec.Command("git", "fetch", "--depth=1", "origin", "main")
	fetch.Dir = root
	if fetch.Run() == nil {
		show = exec.Command("git", "show", "origin/main:.github/workflows/ci.yml")
		show.Dir = root
		if out, err := show.Output(); err == nil {
			return string(out)
		}
	}
	t.Fatalf("cannot read origin/main:.github/workflows/ci.yml to compare against; run with the origin remote present")
	return ""
}
