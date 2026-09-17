package ci

import (
	"path/filepath"
	"strings"
	"testing"
)

// ci_branches_test.go pins the per-event job set: what each trigger runs, and
// that the merge queue's gate runs what a push to dev runs. Rule T (pit stop 4,
// 2026-09-16): the -short hosted-pr leg passed #858 while the full Windows leg
// on the dev push failed three of its new tests (dev runs 35142587149,
// 35143418489). A PR that changes a package with hosted-only tests cannot land
// on the -short result alone, so the group commit must run the full hosted legs
// for the packages it changes.
//
// It reads ci.yml as text, like ci_budget_test.go: the file is the contract and
// there is no YAML library in go.mod.

// TestMergeGroupRunsTheFullHostedLegs is the merge-queue's half of the two
// tiers. ci-ok on a merge_group is THE gate that lands a PR, so it must run the
// full hosted suite — the same legs a push to dev runs, without -short — over
// the packages the group changes and their importers, so the leg stays inside
// the budget. A hosted leg that runs -short on the group, or one that runs full
// but unscoped over the whole tree, is not the gate the dev push is.
func TestMergeGroupRunsTheFullHostedLegs(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	blocks := jobBlocks(src)
	if len(blocks) == 0 {
		t.Fatal("no jobs parsed from ci.yml; the parser is looking in the wrong place")
	}

	hostedLegs := 0
	for name, block := range blocks {
		if !runsOnHosted(block) {
			continue
		}
		if !jobRunsOnEvent(block, "merge_group") {
			continue
		}
		hostedLegs++
		if jobUsesShort(block) {
			t.Errorf("hosted job %q runs on merge_group but passes -short: a package with hosted-only tests cannot land on the -short result alone (Rule T, pit stop 4)", name)
		}
		if !strings.Contains(block, "merge_group.base_sha") {
			t.Errorf("hosted job %q runs on merge_group without scoping to the packages the group changes (no merge_group.base_sha diff): the full hosted suite does not fit the budget unscoped", name)
		}
	}
	if hostedLegs == 0 {
		t.Error("no GitHub-hosted job runs on merge_group: the gate that lands a PR must run what the dev push runs (Rule T, pit stop 4)")
	}
}

// TestSelfHostedShardsSelectThePackagesAChangeTouches pins the self-hosted
// test fan-out's price law: on pull_request and merge_group the shards carry
// only the packages the change touches and their in-module importers, while
// the push to dev and the nightly schedule keep the whole tree. The selection
// reads the diff against the event's own base (pull_request.base.sha,
// merge_group.base_sha); a change that touches no Go package — docs or lisp
// only — selects nothing and the fan-out collapses to one leg that prints
// "nothing to test for this change" and exits 0. Without the selection a docs
// change pays twenty-four shards, which is what held the merge queue.
func TestSelfHostedShardsSelectThePackagesAChangeTouches(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	block := jobBody(src, "test-packages")
	if block == "" {
		t.Fatal("no test-packages job in ci.yml; the self-hosted fan-out has no selection to check")
	}
	if runsOnHosted(block) {
		t.Fatal("test-packages is not the self-hosted fan-out; the test is looking at the wrong job")
	}
	for _, want := range []string{
		"github.event.pull_request.base.sha",
		"github.event.merge_group.base_sha",
		"nothing to test for this change",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("test-packages does not carry %q: the self-hosted shards must select the packages a change touches on pull_request and merge_group and run nothing for a change that touches no Go package", want)
		}
	}
}

// jobBlocks returns each job's body, keyed by job name, with the job's own
// `if:` and steps but without the next job's. It starts at `jobs:` and stops at
// the first top-level key, so the keys under `on:` (`push:`, `pull_request:`)
// are not read as jobs.
func jobBlocks(src string) map[string]string {
	out := make(map[string]string)
	inJobs := false
	cur := ""
	var b strings.Builder
	flush := func() {
		if cur != "" {
			out[cur] = b.String()
			cur = ""
			b.Reset()
		}
	}
	for _, line := range strings.Split(src, "\n") {
		if !strings.HasPrefix(line, " ") && strings.TrimSpace(line) == "jobs:" {
			inJobs = true
			continue
		}
		if !inJobs {
			continue
		}
		if line == "" {
			// Blank lines separate jobs; they do not end the section.
			if cur != "" {
				b.WriteString("\n")
			}
			continue
		}
		if !strings.HasPrefix(line, " ") {
			break
		}
		if m := jobKeyRe.FindStringSubmatch(line); m != nil {
			flush()
			cur = m[1]
			continue
		}
		if cur != "" {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	flush()
	return out
}

// runsOnHosted reports whether a job's runs-on names a GitHub-hosted OS. The
// self-hosted legs name the machine (space, studio), never these.
func runsOnHosted(block string) bool {
	return strings.Contains(block, "ubuntu-latest") ||
		strings.Contains(block, "macos-latest") ||
		strings.Contains(block, "windows-latest")
}

// jobRunsOnEvent reports whether a job's own `if:` guard names the event.
func jobRunsOnEvent(block, event string) bool {
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "    if:") && strings.Contains(line, "github.event_name == '"+event+"'") {
			return true
		}
	}
	return false
}

// jobUsesShort reports whether a job's test command passes -short. Comments are
// stripped first, so prose that names the flag is not read as the flag.
func jobUsesShort(block string) bool {
	for _, line := range strings.Split(block, "\n") {
		code := line
		if j := strings.Index(code, "#"); j >= 0 {
			code = code[:j]
		}
		if strings.Contains(code, "go test") && strings.Contains(code, "-short") {
			return true
		}
	}
	return false
}
