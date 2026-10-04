package ci

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestCertificationPerfRunsEveryDiscoveredPackage holds the full discovery-to-matrix
// chain, serial wall clocks, and strict aggregation under the two-minute cap.
func TestCertificationPerfRunsEveryDiscoveredPackage(t *testing.T) {
	t.Parallel()
	raw := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "certification.yml"))
	var wf struct {
		Jobs map[string]struct {
			ciJob    `yaml:",inline"`
			Env      map[string]string `yaml:"env"`
			Strategy struct {
				FailFast *bool          `yaml:"fail-fast"`
				Matrix   map[string]any `yaml:"matrix"`
			} `yaml:"strategy"`
		} `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(raw), &wf))
	plan, ok := wf.Jobs["perf-plan"]
	require.True(t, ok, "perf discovery must finish before any wall-clock leg starts")
	perf, ok := wf.Jobs["perf"]
	require.True(t, ok)
	assert.Equal(t, "ubuntu-latest", plan.RunsOn)
	assert.Equal(t, "ubuntu-latest", perf.RunsOn)
	assert.Equal(t, twoMinuteCap, plan.TimeoutMinutes)
	assert.Equal(t, twoMinuteCap, perf.TimeoutMinutes)
	assert.Equal(t, "perf-plan", perf.Needs)
	assert.Equal(t, "${{ steps.discover.outputs.matrix }}", plan.Outputs["matrix"])
	assert.Contains(t, jobBody(raw, "perf-plan"), "- name: find the perf-tagged tests\n        id: discover")
	assert.Equal(t, map[string]any{"run": "${{ fromJSON(needs.perf-plan.outputs.matrix) }}"}, perf.Strategy.Matrix,
		"the matrix must take every discovered run, without a filter or extra axis")
	require.NotNil(t, perf.Strategy.FailFast)
	assert.False(t, *perf.Strategy.FailFast, "one failed package must not cancel the others")
	assert.Equal(t, "${{ matrix.run.Package }}", perf.Env["PERF_PACKAGE"])
	assert.Equal(t, "${{ matrix.run.Run }}", perf.Env["PERF_RUN"])
	discover := stepIndex(plan.ciJob, "find the perf-tagged tests")
	vet := stepIndex(plan.ciJob, "vet the perf-tagged tests")
	save := stepIndex(plan.ciJob, "save the perf build cache")
	require.GreaterOrEqual(t, discover, 0)
	require.Greater(t, vet, discover)
	assert.Greater(t, save, vet, "save every compiled and vetted package before the legs restore")
	assert.Equal(t, ciRunner+" perf-tests\n", plan.Steps[discover].Run)
	assert.Equal(t, "go vet -tags perf $PERF_PKGS", plan.Steps[vet].Run)
	test := stepIndex(perf.ciJob, "the wall-clock tests, one at a time")
	require.GreaterOrEqual(t, test, 0)
	assert.Equal(t, `go test -tags perf -count=1 -v -p 1 -parallel 1 -timeout 60s -run "$PERF_RUN" "$PERF_PACKAGE"`, perf.Steps[test].Run)
	needs := certificationOKNeeds(raw)
	aggregate := jobBody(raw, "certification-ok")
	for _, name := range []string{"perf-plan", "perf"} {
		assert.True(t, needs[name], "certification-ok must require %s", name)
		assert.Contains(t, aggregate, name+"=${{ needs."+name+".result }}")
	}
}
