package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// darwinFunctionalTests are the three regressions that
// v17-tools-unit-tier-opens-no-socket moved behind the functional tag. The
// Linux functional job cannot run them: pkg/pkgselect's DarwinOnly keeps
// ./cmd/nova-sandbox and ./pkg/sandbox off every Linux leg, and the hosted
// macOS matrix runs the unit suite without -tags functional. They need one
// macOS runner that passes the tag.
var darwinFunctionalTests = []string{
	"TestNetAllowGrantsExactlyTheNamedLoopbackPort",
	"TestTheCheckScriptPassesAgainstTheToolsProfile",
	"TestTheAgentSocketIsUnreachable",
}

// TestTheDarwinSandboxFunctionalTestsHaveANightlyRunner: ci.yml has a
// functional-darwin job on macos-latest, run only on the nightly schedule and
// by hand, that runs both DarwinOnly packages with -tags functional and the
// three named tests, and proves each one reported --- PASS. ci-ok must not need
// it: certification is nightly only, so this job gates nothing.
func TestTheDarwinSandboxFunctionalTestsHaveANightlyRunner(t *testing.T) {
	t.Parallel()

	const jobName = "functional-darwin"
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))

	jobs := ciJobs(t)
	job, ok := jobs[jobName]
	require.Truef(t, ok, "ci.yml has no %q job: the Darwin sandbox tests moved behind `darwin && functional` run nowhere, because the functional job is Linux and DarwinOnly keeps both packages off its legs", jobName)

	runsOn, err := yaml.Marshal(job.RunsOn)
	require.NoError(t, err)
	assert.Containsf(t, string(runsOn), "macos-latest", "ci.yml job %s runs-on %s, want macos-latest: these tests need a real Darwin sandbox-exec", jobName, runsOn)

	const wantIf = "github.event_name == 'schedule' || github.event_name == 'workflow_dispatch'"
	assert.Equalf(t, wantIf, strings.TrimSpace(job.If), "ci.yml job %s if is %q, want %q: nightly and by hand only, never on a pull request or merge queue", jobName, job.If, wantIf)

	assert.Truef(t, job.TimeoutMinutes > 0 && job.TimeoutMinutes <= twoMinuteCap, "ci.yml job %s timeout-minutes = %d, want a positive value under the %d-minute cap", jobName, job.TimeoutMinutes, twoMinuteCap)

	needs, err := yaml.Marshal(jobs["ci-ok"].Needs)
	require.NoError(t, err)
	assert.NotContainsf(t, string(needs), jobName, "ci-ok needs %s: the nightly-only Darwin functional runner must gate nothing", jobName)

	body := jobBody(src, jobName)
	require.NotEmptyf(t, body, "ci.yml has no readable body for job %s", jobName)
	assert.Containsf(t, body, "actions/checkout@", "ci.yml job %s does not check the tree out through actions/checkout", jobName)
	assert.Containsf(t, body, "actions/setup-go@", "ci.yml job %s does not set up Go through actions/setup-go", jobName)
	assert.Containsf(t, body, "go-version-file: go.mod", "ci.yml job %s does not take the Go version from go.mod", jobName)

	var runs strings.Builder
	for _, step := range job.Steps {
		runs.WriteString(step.Run)
		runs.WriteString("\n")
	}
	run := runs.String()
	// Every build/test command in ci.yml goes through make (TestCIBuildTestLint
	// CommandsGoThroughMake), so the job runs the functional tier's own target
	// over the two DarwinOnly packages; that target is what passes -tags
	// functional and -count=1 (tools/ci functional-run, held by
	// TestFunctionalTierRunsOnlyAsStreamsMerge).
	assert.Containsf(t, run, "make test-functional", "ci.yml job %s does not run the functional tier through make test-functional", jobName)
	for _, want := range []string{"./pkg/sandbox/", "./cmd/nova-sandbox/"} {
		assert.Containsf(t, run, want, "ci.yml job %s never names %s", jobName, want)
	}
	mk := parseMakefile(t, filepath.Join(root, "Makefile"))
	recipe := strings.Join(mk.recipeFor("test-functional"), "\n")
	assert.Containsf(t, recipe, "tools/ci functional-run", "Makefile test-functional no longer runs the functional tier's selection")
	verb := readFile(t, filepath.Join(root, "tools", "ci", "functionalrun.go"))
	for _, want := range []string{`"-tags", "functional"`, `"-count=1"`} {
		assert.Containsf(t, verb, want, "tools/ci/functionalrun.go no longer passes %s to go test", want)
	}
	for _, name := range darwinFunctionalTests {
		assert.Containsf(t, run, name, "ci.yml job %s never names %s", jobName, name)
	}
	for _, name := range darwinFunctionalTests {
		assert.Containsf(t, run, "--- PASS: "+name, "ci.yml job %s does not fail when %s reported no --- PASS", jobName, name)
	}
}
