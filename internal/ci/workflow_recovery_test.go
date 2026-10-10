package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestPathFilterHasCompleteHistoryAndGatesCI(t *testing.T) {
	t.Parallel()
	raw := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	// Decode only the filter: other actions have structured inputs.
	var document struct {
		Jobs map[string]yaml.Node `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(raw), &document))
	filter, ok := document.Jobs["lisp-changes"]
	require.True(t, ok)
	var job struct {
		Steps []struct {
			Uses string            `yaml:"uses"`
			With map[string]string `yaml:"with"`
		} `yaml:"steps"`
	}
	require.NoError(t, filter.Decode(&job))
	checkouts := 0
	for _, step := range job.Steps {
		if strings.HasPrefix(step.Uses, "actions/checkout@") {
			checkouts++
			assert.Equal(t, "0", step.With["fetch-depth"], "path-filter checkout needs complete ancestry")
		}
	}
	assert.Equal(t, 2, checkouts, "both checkout attempts must retain complete history")
	gate := ciJobs(t)["ci-ok"]
	assert.Contains(t, gate.Needs, "lisp-changes")
	verdicts := 0
	for _, step := range gate.Steps {
		if strings.Contains(step.Run, " aggregate ") {
			verdicts++
			assert.Contains(t, step.Run, "lisp-changes=${{ needs.lisp-changes.result }}", step.Name)
		}
	}
	assert.Equal(t, 2, verdicts)
}
