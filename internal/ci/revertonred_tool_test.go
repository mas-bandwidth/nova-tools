package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestRevertOnRedRunsAToolBuiltFromDevNotFromTheRedTree holds revert-on-red.yml
// to one rule: the program that reverts a red push to main is never built from
// the tree being reverted. That push may be the one that broke tools/ci, and a
// revert that cannot build cannot revert it. The workflow checks the red tree
// out at the workspace root (it is what `git revert` acts on) and checks dev's
// tip, the last green source of main, out beside it; setup-go reads that
// checkout's go.mod, and the step builds tools/ci from that checkout alone and
// runs the binary. No step may `go run` or `go build` tools/ci from the root.
func TestRevertOnRedRunsAToolBuiltFromDevNotFromTheRedTree(t *testing.T) {
	t.Parallel()
	type step struct {
		Name string            `yaml:"name"`
		Uses string            `yaml:"uses"`
		Run  string            `yaml:"run"`
		With map[string]string `yaml:"with"`
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []step `yaml:"steps"`
		} `yaml:"jobs"`
	}
	raw := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "revert-on-red.yml"))
	require.NoError(t, yaml.Unmarshal([]byte(raw), &wf), "revert-on-red.yml")
	job, ok := wf.Jobs["revert-on-red"]
	require.True(t, ok, "revert-on-red.yml has no revert-on-red job")
	toolDir, goSetup, built := "", false, false
	for i, s := range job.Steps {
		switch {
		case strings.HasPrefix(s.Uses, "actions/checkout@") && s.With["path"] != "":
			assert.Equal(t, "dev", s.With["ref"], "step %d checks the tool out at another ref than dev (the last green source of main)", i)
			toolDir = s.With["path"]
		case strings.HasPrefix(s.Uses, "actions/checkout@"):
			assert.Contains(t, s.With["ref"], "workflow_run.head_sha", "step %d checks out another ref at the root; the root is the red tree", i)
		case strings.HasPrefix(s.Uses, "actions/setup-go@"):
			assert.True(t, toolDir != "" && s.With["go-version-file"] == toolDir+"/go.mod", "step %d sets up Go from %q; want the tool checkout's go.mod, after that checkout", i, s.With["go-version-file"])
			goSetup = true
		}
		for _, line := range strings.Split(s.Run, "\n") {
			l := strings.TrimSpace(line)
			if !strings.Contains(l, "./tools/ci") {
				continue
			}
			if !assert.True(t, toolDir != "" && strings.HasPrefix(l, "go -C "+toolDir+" build "), "step %d builds or runs tools/ci from the red tree: %q; build it with go -C %s", i, l, toolDir) {
				continue
			}
			built = true
		}
	}
	require.NotEmpty(t, toolDir, "revert-on-red.yml: tool checkout %q, Go set up %t, tool built from it %t; all three are required", toolDir, goSetup, built)
	require.True(t, goSetup, "revert-on-red.yml: tool checkout %q, Go set up %t, tool built from it %t; all three are required", toolDir, goSetup, built)
	require.True(t, built, "revert-on-red.yml: tool checkout %q, Go set up %t, tool built from it %t; all three are required", toolDir, goSetup, built)
}

// TestRevertOnRedTreatsCancelledAsRed holds revert-on-red.yml to one rule: a
// cancelled ci run on a push to main counts as red, so a cold-cache cancellation
// that never ran the tests does not leave an unverified main. The workflow_run
// trigger fires on every completed ci run on main, and the job's `if` names the
// conclusions it proceeds on; cancellation must stand beside failure there, or a
// cancelled run completes with nobody in the room and a bad main is left
// standing (docs/fixes.sexp: update-revert-on-red-cancelled-run).
func TestRevertOnRedTreatsCancelledAsRed(t *testing.T) {
	t.Parallel()
	var wf struct {
		Jobs map[string]struct {
			If string `yaml:"if"`
		} `yaml:"jobs"`
	}
	raw := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "revert-on-red.yml"))
	require.NoError(t, yaml.Unmarshal([]byte(raw), &wf), "revert-on-red.yml")
	ifCond := wf.Jobs["revert-on-red"].If
	require.NotEmpty(t, ifCond, "revert-on-red.yml: the revert-on-red job has no trigger condition")
	assert.Contains(t, ifCond, "conclusion == 'failure'", "revert-on-red.yml: the trigger must proceed on a failed run")
	assert.Contains(t, ifCond, "conclusion == 'cancelled'", "revert-on-red.yml: the trigger must proceed on a cancelled run; a cancelled run is red, not a pass")
}
