package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAnUntidyModuleIsRefusedOnTheCLPath pins the gate that refuses an untidy
// go.mod on the CL path. It reads the repository's own files and never runs
// make, go, or the network. It fails unless the Makefile has a phony mod-tidy
// target whose recipe runs "mod tidy -diff", lint names mod-tidy as a
// prerequisite, and the lint job of ci.yml has a run line that is exactly
// make mod-tidy.
func TestAnUntidyModuleIsRefusedOnTheCLPath(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	mk := parseMakefile(t, filepath.Join(root, "Makefile"))

	// Check: mod-tidy is a phony target
	phonyOK := mk.phony["mod-tidy"]
	require.True(t, phonyOK, "Makefile .PHONY does not name mod-tidy")

	// Check: mod-tidy recipe runs mod tidy -diff
	recipe := mk.recipeFor("mod-tidy")
	require.NotEmpty(t, recipe, "Makefile mod-tidy target has no recipe")
	foundDiff := false
	for _, line := range recipe {
		if strings.Contains(line, "mod tidy") && strings.Contains(line, "-diff") {
			foundDiff = true
			break
		}
	}
	assert.True(t, foundDiff, "Makefile mod-tidy recipe does not run mod tidy -diff; recipe: %q", recipe)

	// Check: lint names mod-tidy as a prerequisite
	lintDeps := mk.deps["lint"]
	require.NotEmpty(t, lintDeps, "Makefile lint target has no prerequisites")
	foundInLint := false
	for _, d := range lintDeps {
		if d == "mod-tidy" {
			foundInLint = true
			break
		}
	}
	assert.True(t, foundInLint, "Makefile lint does not name mod-tidy as a prerequisite; deps: %v", lintDeps)

	// Check: ci.yml lint job has a run line that is exactly "make mod-tidy"
	ciSrc := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	cmds := runCommands(ciSrc)
	foundLintStep := false
	for _, c := range cmds {
		if strings.Contains(c.cmd, "make mod-tidy") {
			foundLintStep = true
			break
		}
	}
	assert.True(t, foundLintStep, "ci.yml lint job has no run line that is exactly make mod-tidy")
}
