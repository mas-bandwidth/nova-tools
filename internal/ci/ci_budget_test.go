package ci

import (
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
//   (b) every job name that left ci.yml in the split is present in the
//       certification workflow by the same name, and certification-ok needs
//       every one of them, so the split deleted nothing;
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
	cert := readFile(t, filepath.Join(root, ".github", "workflows", "certification.yml"))

	certNames := toSet(jobNames(cert))
	if len(certNames) == 0 {
		t.Fatal("no jobs parsed from certification.yml; the parser is looking in the wrong place")
	}
	for _, name := range splitMovedJobs {
		if !certNames[name] {
			t.Errorf("job %q left ci.yml in the split but is not present in certification.yml; the split must delete nothing", name)
		}
	}

	needs := certificationOKNeeds(cert)
	for _, name := range splitMovedJobs {
		if !needs[name] {
			t.Errorf("certification-ok does not list %q in its needs; every certification job must be aggregated", name)
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

// splitMovedJobs is the inventory of the jobs the 2026-09-12 split moved out of
// ci.yml into certification.yml, read off origin/main's ci.yml at the time of
// the split: the whole-tree `-race` test, the Windows build/vet and per-package
// tests, the three-OS smoke, the release dry-run, and the nightly perf wall
// clock. It is checked in as a fixed list on purpose: an ordinary `go test`
// from a source archive or an offline checkout has no origin/main ref to fetch,
// so the comparison must not need one.
var splitMovedJobs = []string{
	"test",
	"build-windows",
	"windows-packages",
	"test-windows",
	"smoke",
	"release-dry-run",
	"perf",
}

// certificationOKNeeds returns the set of job names listed in certification-ok's
// `needs:` line. A job the aggregate forgot to list is a job whose red no longer
// blocks a release, so the needs list is asserted to cover every moved job.
func certificationOKNeeds(src string) map[string]bool {
	needs := make(map[string]bool)
	inCertOK := false
	for _, line := range strings.Split(src, "\n") {
		if m := jobKeyRe.FindStringSubmatch(line); m != nil {
			inCertOK = m[1] == "certification-ok"
			continue
		}
		if !inCertOK {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "needs:") {
			list := strings.TrimSpace(strings.TrimPrefix(trimmed, "needs:"))
			list = strings.Trim(list, "[]")
			for _, name := range strings.Split(list, ",") {
				if name = strings.TrimSpace(name); name != "" {
					needs[name] = true
				}
			}
			return needs
		}
	}
	return needs
}
