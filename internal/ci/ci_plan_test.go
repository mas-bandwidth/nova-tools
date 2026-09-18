package ci

import (
	"path/filepath"
	"strings"
	"testing"
)

// ci_plan_test.go pins the plan job that routes each shard before it exists
// (Glenn 2026-09-17: "If you don't have enough room in the fleet to run CI, it
// is OK to have GitHub run the CI as overflow. Configure this so it's
// automatic. We need the spill over."). GitHub cannot move a job to a different
// runner once it is queued, so the routing has to happen in a plan job whose
// outputs the test jobs read with fromJSON. The arithmetic itself is in
// internal/ci/plan; this test reads ci.yml as text, like the rest of the
// package, and asserts the wiring: the plan is at the head, it can read the
// runners, and the shards and the merge gate read its one output.
func TestPlanJobRoutesShardsBeforeTheyExist(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))

	planJob := jobBody(src, "plan")
	if planJob == "" {
		t.Fatal("no plan job in ci.yml; nothing routes a shard to hosted runners when the fleet is full")
	}
	if !strings.Contains(planJob, "runs-on: ubuntu-latest") {
		t.Error("the plan job does not run on ubuntu-latest; it must run even when every self-hosted runner is busy")
	}
	if !strings.Contains(planJob, "actions: read") {
		t.Error("the plan job does not grant `actions: read`; it cannot list the repository's runners without it")
	}
	if !strings.Contains(planJob, "go run ./internal/ci/cmd/ciplan") {
		t.Error("the plan job does not run the ciplan planner")
	}
	if !strings.Contains(planJob, "runs_on: ${{ steps.plan.outputs.runs_on }}") {
		t.Error("the plan job does not publish runs_on as an output; the shards cannot read it")
	}

	// The plan is the head of the jobs: it must be created before the jobs whose
	// runs-on it feeds.
	if pi, li := strings.Index(src, "\n  plan:"), strings.Index(src, "\n  lint:"); pi < 0 || li < 0 || pi > li {
		t.Errorf("the plan job is not at the head of ci.yml (plan at %d, lint at %d); the routing must exist before the test jobs are created", pi, li)
	}

	// The test matrix reads the plan.
	testJob := jobBody(src, "test")
	if !strings.Contains(testJob, "needs: [plan, test-packages]") {
		t.Errorf("the test job does not need the plan alongside test-packages:\n%s", needsLines(testJob))
	}
	if !strings.Contains(testJob, "fromJSON(needs.plan.outputs.runs_on)[matrix.entry.key]") {
		t.Error("the test job's runs-on does not read the plan's runs_on by matrix.entry.key")
	}

	// The merge gate reads the plan, with the leg's own image as the fallback.
	mergeJob := jobBody(src, "test-hosted-merge")
	if !strings.Contains(mergeJob, "needs: [plan, plan-merge]") {
		t.Errorf("test-hosted-merge does not need the plan:\n%s", needsLines(mergeJob))
	}
	if !strings.Contains(mergeJob, "fromJSON(needs.plan.outputs.runs_on)[matrix.leg.key]") {
		t.Error("test-hosted-merge's runs-on does not read the plan's runs_on by matrix.leg.key")
	}

	// The matrix entries the plan is indexed by carry the keys it emits.
	pkgJob := jobBody(src, "test-packages")
	for _, want := range []string{`\"key\":\"space-$n\"`, `\"key\":\"studio-$n\"`} {
		if !strings.Contains(pkgJob, want) {
			t.Errorf("test-packages does not stamp %s into its matrix entries; the plan output cannot be looked up", want)
		}
	}
}

// TestPlanJobSpillsAreSummarised: a spill is one line in the job summary, and
// the planner writes it. The summary file is named in the program, so the check
// is that the job summary is written at all and the spilled keys are listed.
func TestPlanJobSpillsAreSummarised(t *testing.T) {
	root := repoRoot(t)
	mainSrc := readFile(t, filepath.Join(root, "internal", "ci", "cmd", "ciplan", "main.go"))
	if !strings.Contains(mainSrc, "GITHUB_STEP_SUMMARY") {
		t.Error("ciplan never writes GITHUB_STEP_SUMMARY; a spill would not be one line in the job summary")
	}
	if !strings.Contains(mainSrc, "spill:") {
		t.Error("ciplan does not name the spill line; the summary is the one place a spill is visible")
	}
}

func needsLines(job string) string {
	var out []string
	for _, line := range strings.Split(job, "\n") {
		if strings.Contains(line, "needs:") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
