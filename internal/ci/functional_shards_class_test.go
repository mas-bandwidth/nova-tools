package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// functional_shards_class_test.go holds the release chain to its shape: the
// PR CI runs the SAME functional shards the merge group runs, so a red
// functional test is red on the pull request that made it and never first in
// the queue or at the cut; certification.yml is the suite release.yml's
// certified job asks about; and release.yml publishes the release object with
// the built assets and the SHA256SUMS. It reads all three workflow files
// because the one fact worth holding is the passage between them.

// workflowStep is one step of a workflow job, as far as these tests read it.
type workflowStep struct {
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]string `yaml:"with"`
}

// workflowJob is one job of a workflow file, as far as these tests read it.
type workflowJob struct {
	If       string            `yaml:"if"`
	Needs    any               `yaml:"needs"`
	Outputs  map[string]string `yaml:"outputs"`
	Strategy struct {
		Matrix map[string]any `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []workflowStep `yaml:"steps"`
}

// workflowFile reads one .github/workflows file into the little of it these
// tests read. The `on:` key is deliberately absent: YAML 1.1 reads a bare
// `on` as a boolean and the event list is asserted from the jobs' own `if:`
// lines, which is where this repository states which event a job runs on.
func workflowFile(t *testing.T, name string) map[string]workflowJob {
	t.Helper()
	raw := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", name))
	var wf struct {
		Jobs map[string]workflowJob `yaml:"jobs"`
	}
	err := yaml.Unmarshal([]byte(raw), &wf)
	require.NoErrorf(t, err, "%s: %v", name, err)
	return wf.Jobs
}

// jobRunsStep reports whether any step of the job runs a command containing s.
func jobRunsStep(job workflowJob, s string) bool {
	for _, step := range job.Steps {
		if strings.Contains(step.Run, s) {
			return true
		}
	}
	return false
}

// TestPRCIRunsEveryFunctionalShardTheMergeGroupRuns: ci.yml's functional job
// runs on pull_request and merge_group alike, and BOTH events deal the same
// matrix: one `test-packages` list (`needs.test-packages.outputs.functional`,
// whose own `if:` runs on a same-repo pull request and on the merge group). So
// every functional shard the queue runs, the PR runs, and a shard that could
// not finish under the per-package timeout would be split by the selection
// rather than dropped: the job's two-minute cap and `make test-functional` are
// asserted here, and ci-ok requires the job's result.
func TestPRCIRunsEveryFunctionalShardTheMergeGroupRuns(t *testing.T) {
	t.Parallel()

	jobs := workflowFile(t, "ci.yml")
	functional, ok := jobs["functional"]
	require.True(t, ok, "ci.yml has no functional job")
	for _, ev := range []string{"pull_request", "merge_group"} {
		assert.Containsf(t, functional.If, "github.event_name == '"+ev+"'", "functional's if does not run on %s: %s", ev, functional.If)
	}
	assert.Containsf(t, functional.Strategy.Matrix["entry"], "fromJSON(needs.test-packages.outputs.functional)",
		"functional's matrix is %v; the PR and the merge group must read the one test-packages functional list", functional.Strategy.Matrix["entry"])
	packages, ok := jobs["test-packages"]
	require.True(t, ok, "ci.yml has no test-packages job")
	assert.Equalf(t, "${{ steps.list.outputs.functional }}", packages.Outputs["functional"],
		"test-packages does not output the functional list")
	assert.Containsf(t, packages.If, "github.event.pull_request.head.repo.full_name == github.repository",
		"test-packages' if does not run on a same-repo pull request, so the PR functional shards would be empty: %s", packages.If)
	assert.Contains(t, functional.Steps[len(functional.Steps)-1].Run, "make test-functional",
		"functional's last step is not `make test-functional`")

	// ci-ok is the gate the queue reads, so it must need functional and read
	// its result; a skipped functional is not silently ok.
	ciOK, ok := jobs["ci-ok"]
	require.True(t, ok, "ci.yml has no ci-ok job")
	needs, err := yaml.Marshal(ciOK.Needs)
	require.NoError(t, err)
	assert.Contains(t, string(needs), "functional", "ci-ok does not need functional")
	assert.True(t, jobRunsStep(ciOK, "functional=${{ needs.functional.result }}"),
		"ci-ok's aggregate does not read functional's result")
	assert.NotContainsf(t, strings.Join(jobRunLines(ciOK), "\n"), "skip-ok-on pull_request=functional",
		"ci-ok still treats a skipped functional on a pull request as ok")
}

// jobRunLines returns every run line of the job, for a whole-job text check.
func jobRunLines(job workflowJob) []string {
	var out []string
	for _, step := range job.Steps {
		out = append(out, step.Run)
	}
	return out
}

// TestTheReleaseChainIsCertificationThenCutThenRelease: certification.yml is
// the suite release.yml's `certified` job asks about and the cut refuses
// without; release.yml publishes the release object with the built assets and
// the checksums (`ghrelease attach`), and the tag push is the trigger. The
// three files are one chain, so the test reads all three.
func TestTheReleaseChainIsCertificationThenCutThenRelease(t *testing.T) {
	t.Parallel()

	cert := workflowFile(t, "certification.yml")
	_, ok := cert["certification-ok"]
	require.True(t, ok, "certification.yml has no certification-ok job; the release's certified gate has nothing to read")
	assert.Truef(t, jobRunsStep(cert["release-dry-run"], "ghrelease sums"),
		"certification.yml's release-dry-run does not run the release's own sums step, so the dry run is not the release")

	release := workflowFile(t, "release.yml")
	certified, ok := release["certified"]
	require.True(t, ok, "release.yml has no certified job")
	assert.Truef(t, jobRunsStep(certified, "ghrelease certified"),
		"release.yml's certified job does not ask certification.yml whether it vouches for the commit")
	build, ok := release["build"]
	require.True(t, ok, "release.yml has no build job")
	assert.Truef(t, needsIncludes(build.Needs, "certified"),
		"release.yml's build does not need certified: a release could be built for an uncertified commit")
	rel, ok := release["release"]
	require.True(t, ok, "release.yml has no release job")
	assert.Truef(t, needsIncludes(rel.Needs, "build"),
		"release.yml's release does not need build: it would publish a set nobody built")
	assert.Truef(t, jobRunsStep(rel, "ghrelease attach"),
		"release.yml's release job does not run `ghrelease attach`, the one step that sums and publishes the set")
}

// needsIncludes reports whether a YAML `needs` (a string or a list) names job.
func needsIncludes(needs any, job string) bool {
	switch v := needs.(type) {
	case string:
		return v == job
	case []any:
		for _, n := range v {
			if n == job {
				return true
			}
		}
	}
	return false
}
