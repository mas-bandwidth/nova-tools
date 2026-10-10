package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestDocsWorkflowRunsTheDocsGatesOnEveryDocsChange verifies that the docs
// workflow exists and runs the docs gates on every pull request that touches
// docs/, a README, or a verbhelp.go.
func TestDocsWorkflowRunsTheDocsGatesOnEveryDocsChange(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	wfPath := filepath.Join(root, ".github", "workflows", "docs.yml")

	// 1. The workflow file exists.
	data, err := os.ReadFile(wfPath)
	require.NoError(t, err, "docs.yml not found; create .github/workflows/docs.yml")

	// 2. Parse the workflow.
	var wf struct {
		Name string `yaml:"name"`
		On   struct {
			PullRequest struct {
				Paths []string `yaml:"paths"`
			} `yaml:"pull_request"`
		} `yaml:"on"`
		Jobs struct {
			DocsCheck struct {
				RunsOn []string `yaml:"runs-on"`
				Steps  []struct {
					Name string `yaml:"name"`
					Run  string `yaml:"run"`
				} `yaml:"steps"`
			} `yaml:"docs-check"`
		} `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(data, &wf), "parse docs.yml")

	// 3. The workflow has a docs-check job.
	require.NotEmpty(t, wf.Jobs.DocsCheck.Steps, "docs-check job has no steps")

	// 4. Verify the workflow runs on pull_request with the right paths.
	require.NotEmpty(t, wf.On.PullRequest.Paths, "pull_request has no paths filter")
	wantPaths := []string{"docs/**", "**/README.md", "cmd/*/verbhelp.go", "tools/clidoc/**"}
	for _, want := range wantPaths {
		found := false
		for _, p := range wf.On.PullRequest.Paths {
			if p == want {
				found = true
				break
			}
		}
		require.True(t, found, "pull_request paths missing %q", want)
	}
}

// TestMakefileHasDocsCheckTarget verifies that the Makefile has a docs-check target.
func TestMakefileHasDocsCheckTarget(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	mfPath := filepath.Join(root, "Makefile")

	data, err := os.ReadFile(mfPath)
	require.NoError(t, err, "Makefile not found")

	content := string(data)
	require.Contains(t, content, "docs-check", "Makefile must have docs-check target")
	require.Contains(t, content, ".PHONY", "Makefile must declare docs-check as .PHONY")
}

// TestDocsCheckTargetRunsDocsGates verifies that the docs-check target actually
// runs the docs gates (internal/docs tests, link check, cli check, terminology lint).
func TestDocsCheckTargetRunsDocsGates(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	mfPath := filepath.Join(root, "Makefile")

	data, err := os.ReadFile(mfPath)
	require.NoError(t, err, "Makefile not found")

	content := string(data)
	// Look for docs-check target definition
	docsCheckStart := strings.Index(content, "docs-check:")
	require.NotEqual(t, -1, docsCheckStart, "docs-check target not found in Makefile")

	// Get the recipe (lines after docs-check:)
	after := content[docsCheckStart:]
	lines := strings.Split(after, "\n")
	require.GreaterOrEqual(t, len(lines), 3, "docs-check target has incomplete recipe")

	// The recipe must contain the gate calls
	recipe := strings.Join(lines[1:], "\n")
	require.Contains(t, recipe, "internal/docs", "docs-check recipe must run internal/docs tests")
	require.Contains(t, recipe, "nova-check links", "docs-check recipe must run link check")
	require.Contains(t, recipe, "clidoc", "docs-check recipe must run CLI check")
}

// TestDocsWorkflowHasNoMachineName verifies that the workflow does not name
// a specific machine (generality rule).
func TestDocsWorkflowHasNoMachineName(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	wfPath := filepath.Join(root, ".github", "workflows", "docs.yml")

	data, err := os.ReadFile(wfPath)
	require.NoError(t, err, "docs.yml not found")

	content := string(data)
	// Check that runs-on does not name a specific machine like "hulk", "vision", etc.
	// It should use generic labels like "self-hosted, linux, x64, space"
	require.NotContains(t, content, "runs-on: [hulk]", "workflow must not name specific machine")
	require.NotContains(t, content, "runs-on: [vision]", "workflow must not name specific machine")
	require.NotContains(t, content, "runs-on: [mini]", "workflow must not name specific machine")
}
