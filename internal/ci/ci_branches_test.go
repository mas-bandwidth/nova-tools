package ci

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ci_branches_test.go pins the "one fact named once" shape of ci.yml for two
// facts that drift as literals: WHICH branches are the integration branches,
// and HOW MANY runners share each self-hosted machine. Rule B of #828 is "one
// fact written twice": written in more than one place, one edit updates one and
// not the other, and the tool that described a fact no longer describes the
// tree. Each fact is held to one list or one environment variable, and this
// test is the law that the literals are gone.

// TestIntegrationBranchesAreOneList is rule B for the integration branches.
// main and dev are named exactly once, in the INTEGRATION_BRANCHES env list;
// every rule that needs to know which branches integrate reads that list
// through contains(fromJSON(...)) rather than carrying its own
// refs/heads/main or refs/heads/dev literal.
func TestIntegrationBranchesAreOneList(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	lines := strings.Split(src, "\n")

	// ONE list, named once in an env binding.
	var list string
	for _, line := range lines {
		if strings.HasPrefix(line, "  INTEGRATION_BRANCHES:") {
			list = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "  INTEGRATION_BRANCHES:")), "'\"")
			break
		}
	}
	if list == "" {
		t.Fatal("no INTEGRATION_BRANCHES env list in ci.yml; the integration branches are not named once")
	}

	// Both integration branches are in the one list, or the list is not the
	// single source for one of them.
	for _, branch := range []string{"main", "dev"} {
		if !strings.Contains(list, `"`+branch+`"`) {
			t.Errorf("INTEGRATION_BRANCHES %s does not name %q; that branch is a special case written somewhere else", list, branch)
		}
	}

	// No rule may carry its own refs/heads/main or refs/heads/dev literal:
	// those are the "one fact written twice" that drift from the list.
	for i, line := range lines {
		for _, ref := range []string{"refs/heads/main", "refs/heads/dev"} {
			if strings.Contains(line, ref) {
				t.Errorf("ci.yml:%d still names %q as a literal; every rule must read INTEGRATION_BRANCHES via contains(fromJSON(...))", i+1, ref)
			}
		}
	}

	// The two rules that can read the list do: each is gated on membership of
	// github.ref_name in the list, so a branch added to the list is treated by
	// both (keyed on the sha, and not cancelled) with no other edit.
	want := "contains(fromJSON(env.INTEGRATION_BRANCHES), github.ref_name)"
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "group:"):
			if !strings.Contains(line, want) {
				t.Errorf("ci.yml:%d: concurrency group does not read the list; want %q in %q", i+1, want, trimmed)
			}
		case strings.HasPrefix(trimmed, "cancel-in-progress:"):
			if !strings.Contains(line, want) {
				t.Errorf("ci.yml:%d: cancel-in-progress does not read the list; want %q in %q", i+1, want, trimmed)
			}
		}
	}

	// `on` cannot evaluate an expression, so the push filter names the branches
	// a second and final time; that line must stay in agreement with the list,
	// and it is the only place the bare names may appear.
	if !strings.Contains(src, "branches: [main, dev]") {
		t.Errorf("on.push.branches must stay in agreement with the list as exactly `branches: [main, dev]`")
	}
}

// TestFairShareHasNoLiteralDivisor is rule B for the runners-per-machine count.
// The fair-share step reads NOVA_RUNNERS_PER_MACHINE from the runner's
// environment (default 8) and prints which value it used, so the number lives
// on the machines that set it rather than written again as a literal divisor in
// the step.
func TestFairShareHasNoLiteralDivisor(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))

	if !strings.Contains(src, `runners="${NOVA_RUNNERS_PER_MACHINE:-8}"`) {
		t.Errorf("the fair-share step must read the runner count from NOVA_RUNNERS_PER_MACHINE with a default of 8")
	}
	if !strings.Contains(src, "share=$(( cores / runners ))") {
		t.Errorf("the fair-share step must divide cores by the runner count it read, not by a literal")
	}
	// No other literal divisor may remain: once the count lives in the
	// environment, a second hardcoded number is the drift rule B is about.
	if m := regexp.MustCompile(`cores\s*/\s*[0-9]+`).FindString(src); m != "" {
		t.Errorf("the fair-share step still divides cores by a literal (%q), not by NOVA_RUNNERS_PER_MACHINE", m)
	}
	// And it says which count it used, so a machine with the wrong value is
	// visible in the log rather than silently taking the wrong share.
	if !strings.Contains(src, "$runners runners per machine") || !strings.Contains(src, "this leg takes $share") {
		t.Errorf("the fair-share step must print the runner count it used")
	}
}
