package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// docsWorkflowFile is the docs workflow this card adds; docsSpecPath is the
// specification its class-rule entry belongs under.
const docsWorkflowFile = ".github/workflows/docs.yml"

// docsGatePaths are the paths a docs or help change touches. The workflow runs
// on every one of them, on pull requests and on pushes alike.
var docsGatePaths = []string{"docs/**", "**/README.md", "cmd/*/verbhelp.go", "tools/clidoc/**"}

// docsGateCommands are the gates make docs-check runs, one per recipe line:
// the internal/docs tests, the link check and the generated CLI check.
var docsGateCommands = []string{"./internal/docs", "nova-check links", "clidoc"}

// docsShellControlFlow is the shell vocabulary a step or a recipe line may not
// carry: the card's rule is one command per step and per Makefile line, so a
// branch, a loop, a sequence or an alternative is refused.
var docsShellControlFlow = []string{"if ", "for ", "&&", "||", ";"}

// docsRunnerLabels are the only literal runs-on labels a generic self-hosted
// Linux job needs; a machine or pool name (the fleet's own runner names) is not
// among them.
var docsRunnerLabels = map[string]bool{
	"self-hosted": true,
	"linux":       true,
	"x64":         true,
	"arm64":       true,
	"amd64":       true,
}

// docsTrigger is one event's path filter.
type docsTrigger struct {
	Paths []string `yaml:"paths"`
}

// docsStep is one step: a pinned action or one command.
type docsStep struct {
	Name string `yaml:"name"`
	Uses string `yaml:"uses"`
	Run  string `yaml:"run"`
}

// docsJob is the docs-check job.
type docsJob struct {
	RunsOn         any        `yaml:"runs-on"`
	TimeoutMinutes int        `yaml:"timeout-minutes"`
	Steps          []docsStep `yaml:"steps"`
}

// TestDocsWorkflowRunsTheDocsGatesOnEveryDocsChange holds docs.yml and the
// Makefile target it runs to the docs CI contract:
//
//   - it fires on pull requests and pushes touching docs/**, a README,
//     cmd/*/verbhelp.go or tools/clidoc/**;
//   - its one job keeps the permanent two-minute cap and names only generic
//     self-hosted Linux labels, never a machine;
//   - every step is one command (or one pinned action) with no shell control
//     flow;
//   - make docs-check runs the internal/docs tests, the link check and the
//     generated CLI check, one command per recipe line.
//
// The rule and its entry: docs/SPEC-CI.md#the-class-tests.
func TestDocsWorkflowRunsTheDocsGatesOnEveryDocsChange(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(docsWorkflowFile)))
	require.NoError(t, err, "%s: the docs CI workflow is missing", docsWorkflowFile)

	var wf struct {
		On struct {
			Push        docsTrigger `yaml:"push"`
			PullRequest docsTrigger `yaml:"pull_request"`
		} `yaml:"on"`
		Jobs map[string]docsJob `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(data, &wf), "%s: parse", docsWorkflowFile)

	// Every docs-tier path fires the workflow, on both events.
	for _, ev := range []struct {
		name    string
		trigger docsTrigger
	}{{"pull_request", wf.On.PullRequest}, {"push", wf.On.Push}} {
		for _, want := range docsGatePaths {
			assert.Contains(t, ev.trigger.Paths, want, "%s: %s paths missing %q", docsWorkflowFile, ev.name, want)
		}
	}

	job, ok := wf.Jobs["docs-check"]
	require.True(t, ok, "%s: no docs-check job", docsWorkflowFile)
	assert.NotEmpty(t, job.Steps, "%s: docs-check job has no steps", docsWorkflowFile)
	assert.Equal(t, 2, job.TimeoutMinutes, "%s: docs-check job must keep the permanent two-minute cap", docsWorkflowFile)

	// A generic runner only: no machine or pool name.
	labels := runnerLabels(job.RunsOn)
	require.NotEmpty(t, labels, "%s: docs-check job has no runs-on", docsWorkflowFile)
	for _, label := range labels {
		if strings.Contains(label, "${{") {
			continue // a matrix or expression resolves to a genericity-checked label
		}
		assert.True(t, docsRunnerLabels[label], "%s: runs-on names %q, which is not a generic self-hosted Linux label; a workflow names no machine", docsWorkflowFile, label)
	}

	// One command per step, no shell control flow, and one step runs the target.
	sawMake := false
	for _, step := range job.Steps {
		if step.Run == "" {
			continue
		}
		if strings.TrimSpace(step.Run) == "make docs-check" {
			sawMake = true
		}
		for _, op := range docsShellControlFlow {
			assert.NotContains(t, step.Run, op, "%s: step %q carries shell control flow %q; a step is one command", docsWorkflowFile, step.Name, op)
		}
	}
	assert.True(t, sawMake, "%s: no step runs `make docs-check`", docsWorkflowFile)

	// The Makefile target is the docs gates, one command per line.
	recipe := makeRecipe(t, filepath.Join(root, "Makefile"), "docs-check")
	require.NotEmpty(t, recipe, "Makefile: no docs-check target")
	for _, gate := range docsGateCommands {
		assert.Contains(t, strings.Join(recipe, "\n"), gate, "Makefile docs-check: missing the %q gate", gate)
	}
	for _, line := range recipe {
		for _, op := range docsShellControlFlow {
			assert.NotContains(t, line, op, "Makefile docs-check: recipe line %q carries shell control flow %q; a line is one command", line, op)
		}
	}
}

// runnerLabels reads a runs-on value, a scalar label or a list, as its labels.
func runnerLabels(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{strings.TrimSpace(t)}
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	}
	return nil
}

// makeRecipe returns the tab-led recipe lines of one Makefile target, stopping
// at the first line that is not part of it.
func makeRecipe(t *testing.T, path, target string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, "Makefile: %v", err)
	lines := strings.Split(string(data), "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, target+":") {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	var recipe []string
	for _, line := range lines[start+1:] {
		if !strings.HasPrefix(line, "\t") {
			break
		}
		recipe = append(recipe, strings.TrimSpace(line))
	}
	return recipe
}
