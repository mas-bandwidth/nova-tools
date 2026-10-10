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

// docsCheckPaths are the changes that run the docs gates: documentation, every
// README, the per-verb help, and the CLI reference generator.
var docsCheckPaths = []string{"docs/**", "**/README.md", "cmd/*/verbhelp.go", "tools/clidoc/**"}

// docsCheckMachineNames are the fleet's machine, host and group names. A runner
// label drawn from this set is a machine, not a generic capability; the workflow
// must select generic labels so it runs wherever the fleet puts Linux runners.
var docsCheckMachineNames = map[string]bool{
	"space": true, "hetzner": true, "hulk": true, "vision": true,
	"batman": true, "superman": true, "studio": true, "mini": true,
	"captainamerica": true, "antman": true, "macbook": true,
}

// docsShellControlRe matches shell control flow a step or a recipe line must not
// carry: the `if` and `for` keywords, and the `&&`, `||` and `;` separators.
var docsShellControlRe = regexp.MustCompile(`(^|[^A-Za-z0-9_])(if|for)([^A-Za-z0-9_]|$)|&&|\|\||;`)

// TestDocsWorkflowRunsTheDocsGatesOnEveryDocsChange holds the docs CI to its
// contract: on every pull request and push that touches docs/**, a README, a
// verbhelp.go or tools/clidoc/**, `.github/workflows/docs.yml` runs
// `make docs-check`, a Makefile target that runs the internal/docs tests, the
// link check and the generated CLI check. It fails while a gate is missing,
// while a workflow step or a docs-check recipe line carries shell control flow
// (if, for, &&, ||, ;), or while the workflow names a machine.
func TestDocsWorkflowRunsTheDocsGatesOnEveryDocsChange(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

	// ---- .github/workflows/docs.yml: the trigger and the runner ----
	wfPath := filepath.Join(root, ".github", "workflows", "docs.yml")
	raw, err := os.ReadFile(wfPath)
	require.NoError(t, err, "docs.yml not found; create .github/workflows/docs.yml")

	var wf struct {
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
	require.NoError(t, yaml.Unmarshal(raw, &wf), "parse docs.yml")

	// Both events carry the four paths, so a docs-only change can never land
	// without the gates.
	for _, ev := range []struct {
		name  string
		paths []string
	}{{"push", wf.On.Push.Paths}, {"pull_request", wf.On.PullRequest.Paths}} {
		require.NotEmpty(t, ev.paths, "docs.yml %s has no paths filter", ev.name)
		for _, want := range docsCheckPaths {
			assert.Contains(t, ev.paths, want, "docs.yml %s paths must include %q", ev.name, want)
		}
	}

	// One docs-check job.
	job, ok := wf.Jobs["docs-check"]
	require.True(t, ok, "docs.yml has no docs-check job")
	require.NotEmpty(t, job.Steps, "docs-check job has no steps")

	// The runner is a generic capability, never a machine name.
	require.Contains(t, job.RunsOn, "self-hosted", "docs-check runs-on %v must be self-hosted", job.RunsOn)
	require.Contains(t, job.RunsOn, "linux", "docs-check runs-on %v must be linux", job.RunsOn)
	for _, label := range job.RunsOn {
		assert.False(t, docsCheckMachineNames[label], "docs-check runs-on names a machine %q; use generic labels", label)
	}

	// Every step is one command, and one of them runs `make docs-check`.
	ranDocsCheck := false
	for _, step := range job.Steps {
		if step.Run == "" {
			continue
		}
		assert.NotContains(t, step.Run, "\n", "step %q runs more than one command: %q", step.Name, step.Run)
		assert.NotRegexp(t, docsShellControlRe, step.Run, "step %q carries shell control flow: %q", step.Name, step.Run)
		assert.True(t, strings.HasPrefix(step.Run, "make ") || strings.HasPrefix(step.Run, "go "),
			"step %q is not one command of a Go tool or make: %q", step.Name, step.Run)
		if strings.Contains(step.Run, "make docs-check") {
			ranDocsCheck = true
		}
	}
	assert.True(t, ranDocsCheck, "docs.yml has no step running `make docs-check`")

	// ---- Makefile: the docs-check target and the three gates ----
	mfRaw, err := os.ReadFile(filepath.Join(root, "Makefile"))
	require.NoError(t, err, "Makefile not found")

	recipe := docsCheckRecipe(string(mfRaw))
	require.NotEmpty(t, recipe, "Makefile has no docs-check target with a recipe")

	joined := strings.Join(recipe, "\n")
	assert.Contains(t, joined, "internal/docs", "docs-check must run the internal/docs tests")
	assert.Contains(t, joined, "nova-check links", "docs-check must run the link check")
	assert.Contains(t, joined, "clidoc", "docs-check must run the generated CLI check")

	for _, line := range recipe {
		assert.NotRegexp(t, docsShellControlRe, line, "docs-check recipe line carries shell control flow: %q", line)
		assert.True(t, strings.HasPrefix(line, "$(GO) ") || strings.HasPrefix(line, "$(MAKE) "),
			"docs-check recipe line is not one command of a Go tool or make: %q", line)
	}
}

// docsCheckRecipe returns the recipe lines of the Makefile's docs-check target:
// the tab-indented lines after `docs-check:` until the next non-indented line.
func docsCheckRecipe(mk string) []string {
	lines := strings.Split(mk, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "docs-check:") {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	var recipe []string
	for _, line := range lines[start+1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, "\t") {
			break
		}
		recipe = append(recipe, strings.TrimSpace(line))
	}
	return recipe
}
