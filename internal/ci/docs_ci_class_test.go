package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// docsWorkflowFile is the docs CI workflow: the docs gates run on every change
// that touches the docs tier, so a docs or help change cannot land without
// them.
const docsWorkflowFile = ".github/workflows/docs.yml"

// docsWorkflowPaths are the changes the docs workflow triggers on: the docs
// tree, every README, a verb's help, and the generated CLI reference's tools.
var docsWorkflowPaths = []string{"docs/**", "**/README.md", "cmd/*/verbhelp.go", "tools/clidoc/**"}

// docsWorkflowLabels are the generic runner labels the docs job may name. A
// label outside this set is a machine name, and no workflow names one.
var docsWorkflowLabels = []string{"self-hosted", "linux", "x64"}

// docsWorkflow is the part of the workflow the test reads: its triggers, its
// docs-check job, and that job's steps.
type docsWorkflow struct {
	On struct {
		Push struct {
			Paths []string `yaml:"paths"`
		} `yaml:"push"`
		PullRequest struct {
			Paths []string `yaml:"paths"`
		} `yaml:"pull_request"`
	} `yaml:"on"`
	Jobs map[string]struct {
		RunsOn []string `yaml:"runs-on"`
		Steps  []struct {
			Name string `yaml:"name"`
			Run  string `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// docsBranchTokenRe matches a shell branch or loop keyword at a command
// boundary, so the word in a path or a step name is not a finding.
var docsBranchTokenRe = regexp.MustCompile(`(^|[\s;&|])(if|for)\s`)

// docsShellControlFlow returns the run lines that carry shell control flow: a
// branch, a loop, a command chain or a separator. A docs gate is one command,
// so a step or recipe line that carries a shell script is a finding.
func docsShellControlFlow(run string) []string {
	var found []string
	for _, line := range strings.Split(run, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if docsBranchTokenRe.MatchString(trimmed) ||
			strings.Contains(trimmed, "&&") ||
			strings.Contains(trimmed, "||") ||
			strings.Contains(trimmed, ";") {
			found = append(found, trimmed)
		}
	}
	return found
}

// docsCheckRecipe returns the recipe lines of the Makefile's docs-check target,
// from the target line to the first line that is not part of the recipe.
func docsCheckRecipe(t *testing.T, root string) []string {
	t.Helper()

	lines := strings.Split(readFile(t, filepath.Join(root, "Makefile")), "\n")
	start := -1
	for i, line := range lines {
		if line == "docs-check:" {
			start = i
			break
		}
	}
	require.NotEqual(t, -1, start, "Makefile declares no docs-check target: the docs gates have no single entry")
	var recipe []string
	for _, line := range lines[start+1:] {
		if !strings.HasPrefix(line, "\t") {
			break
		}
		if body := strings.TrimSpace(line); body != "" && !strings.HasPrefix(body, "#") {
			recipe = append(recipe, body)
		}
	}
	require.NotEmpty(t, recipe, "Makefile docs-check target has no recipe")
	return recipe
}

// TestDocsWorkflowRunsTheDocsGatesOnEveryDocsChange holds the docs CI workflow
// and its Makefile target to the card: pull requests and pushes that touch the
// docs tier run `make docs-check`; the target runs the internal/docs tests, the
// link check and the generated CLI check; every step and recipe line is one
// command with no shell control flow; and the job runs on a generic Linux
// runner and never a machine name.
func TestDocsWorkflowRunsTheDocsGatesOnEveryDocsChange(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

	// The workflow exists and parses.
	raw, err := os.ReadFile(filepath.Join(root, docsWorkflowFile))
	require.NoError(t, err, "%s must exist: the docs gates run on every change that touches docs or help", docsWorkflowFile)

	var wf docsWorkflow
	require.NoError(t, yaml.Unmarshal(raw, &wf), "parse %s", docsWorkflowFile)

	// Both events trigger on the docs and help paths.
	for _, ev := range []struct {
		name  string
		paths []string
	}{
		{"push", wf.On.Push.Paths},
		{"pull_request", wf.On.PullRequest.Paths},
	} {
		for _, want := range docsWorkflowPaths {
			assert.Contains(t, ev.paths, want, "%s: %s does not trigger on %q; a docs or help change would land unguarded", docsWorkflowFile, ev.name, want)
		}
	}

	// The docs-check job.
	job, ok := wf.Jobs["docs-check"]
	require.True(t, ok, "%s must declare the docs-check job", docsWorkflowFile)

	// A generic Linux runner, never a machine name.
	require.NotEmpty(t, job.RunsOn, "%s: the docs-check job has no runs-on", docsWorkflowFile)
	for _, want := range []string{"self-hosted", "linux", "x64"} {
		assert.Contains(t, job.RunsOn, want, "%s: docs-check does not name the generic runner label %q", docsWorkflowFile, want)
	}
	assert.Subset(t, docsWorkflowLabels, job.RunsOn, "%s: docs-check runs on %v, which names a machine; use only the generic labels %v", docsWorkflowFile, job.RunsOn, docsWorkflowLabels)

	// Every step is one command with no shell control flow, and one step runs
	// the docs target.
	require.NotEmpty(t, job.Steps, "%s: the docs-check job has no steps", docsWorkflowFile)
	runsMake := false
	for _, step := range job.Steps {
		assert.Empty(t, docsShellControlFlow(step.Run), "%s: step %q carries shell control flow: %q", docsWorkflowFile, step.Name, step.Run)
		if strings.TrimSpace(step.Run) == "make docs-check" {
			runsMake = true
		}
	}
	assert.True(t, runsMake, "%s: no step runs `make docs-check`", docsWorkflowFile)

	// The Makefile target runs the three docs gates, one command each, with no
	// shell control flow.
	recipe := docsCheckRecipe(t, root)
	joined := strings.Join(recipe, "\n")
	for _, gate := range []struct{ name, want string }{
		{"the internal/docs tests", "internal/docs"},
		{"the link check", "nova-check links"},
		{"the generated CLI check", "clidoc"},
	} {
		assert.Contains(t, joined, gate.want, "make docs-check does not run %s (%q):\n%s", gate.name, gate.want, joined)
	}
	for _, line := range recipe {
		assert.Empty(t, docsShellControlFlow(line), "make docs-check recipe carries shell control flow: %q", line)
	}
}
