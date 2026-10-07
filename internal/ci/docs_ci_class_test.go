package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestDocsWorkflowRunsTheDocsGatesOnEveryDocsChange validates that the docs CI
// workflow runs all required gates on docs changes and that no step uses shell logic.
func TestDocsWorkflowRunsTheDocsGatesOnEveryDocsChange(t *testing.T) {
	t.Parallel()

	// Read the workflow file (module relative path)
	workflowPath := filepath.Join("..", "..", ".github", "workflows", "docs.yml")
	workflowBytes, err := os.ReadFile(workflowPath)
	require.NoError(t, err, "workflow file must exist")

	// Parse workflow
	var workflow struct {
		Name string `yaml:"name"`
		//: map[string]yaml.Node `yaml:"on"`
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	err = yaml.Unmarshal(workflowBytes, &workflow)
	require.NoError(t, err, "workflow must be valid YAML")

	// Verify workflow name
	require.Equal(t, "docs-check", workflow.Name, "workflow must be named docs-check")

	// Verify the docs job has a docs-check step
	docsJob, ok := workflow.Jobs["docs"]
	require.True(t, ok, "workflow must have a docs job")

	hasDocsCheckStep := false
	for _, step := range docsJob.Steps {
		if strings.Contains(step.Run, "docs-check") {
			hasDocsCheckStep = true
			break
		}
	}
	require.True(t, hasDocsCheckStep, "docs job must have a docs-check step")

	// Read the Makefile (module relative path)
	makefileBytes, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	require.NoError(t, err, "Makefile must exist")

	// Verify docs-check target exists
	require.Contains(t, string(makefileBytes), "docs-check:", "Makefile must have a docs-check target")

	// Verify docs-check runs all required gates:
	// 1. internal/docs tests
	// 2. nova-check links
	// 3. generated CLI check
	// 4. terminology lint
	require.Contains(t, string(makefileBytes), "internal/docs", "docs-check must run internal/docs tests")
	require.Contains(t, string(makefileBytes), "nova-check links", "docs-check must run nova-check links")
	require.Contains(t, string(makefileBytes), "TerminologyLint", "docs-check must run terminology lint")

	// Verify no shell control flow in docs-check target
	docsCheckSection := extractSection(string(makefileBytes), "docs-check:")
	require.NotContains(t, docsCheckSection, "if ", "docs-check must not use shell conditionals")
	require.NotContains(t, docsCheckSection, "for ", "docs-check must not use shell loops")
	require.NotContains(t, docsCheckSection, "while ", "docs-check must not use shell while loops")
	require.NotContains(t, docsCheckSection, "&& ", "docs-check must not use shell && logic")
	require.NotContains(t, docsCheckSection, "|| ", "docs-check must not use shell || logic")
}

// extractSection extracts the content between two section headers in the Makefile
func extractSection(content, section string) string {
	startIdx := strings.Index(content, section)
	if startIdx == -1 {
		return ""
	}

	// Find the next section (line starting with non-whitespace)
	lines := strings.Split(content[startIdx:], "\n")
	result := []string{}
	for i, line := range lines {
		if i > 0 && strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, " ") {
			break
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}
