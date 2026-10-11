package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// modtidy_class_test.go holds the class test that refuses an untidy go.mod
// from landing on the CL path. It reads the tree's own Makefile and ci.yml
// as text and refuses when the mod-tidy gate is missing.

func TestAnUntidyModuleIsRefusedOnTheCLPath(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

	// Check Makefile has a phony mod-tidy target whose recipe runs "mod tidy -diff"
	mk := parseMakefile(t, filepath.Join(root, "Makefile"))
	modTidyRecipe := mk.recipeFor("mod-tidy")
	require.NotEmpty(t, modTidyRecipe, "Makefile has no mod-tidy target; add a phony target with recipe that runs $(GO) mod tidy -diff")
	foundTidyDiff := false
	for _, line := range modTidyRecipe {
		if strings.Contains(line, "mod tidy -diff") {
			foundTidyDiff = true
			break
		}
	}
	assert.True(t, foundTidyDiff, "Makefile mod-tidy target recipe does not run 'mod tidy -diff'; recipe: %v", modTidyRecipe)
	assert.True(t, mk.phony["mod-tidy"], "Makefile .PHONY does not name mod-tidy")

	// Check lint depends on mod-tidy
	lintDeps := mk.deps["lint"]
	foundLintDep := false
	for _, d := range lintDeps {
		if d == "mod-tidy" {
			foundLintDep = true
			break
		}
	}
	assert.True(t, foundLintDep, "Makefile lint target does not have mod-tidy as a prerequisite")

	// Check ci.yml lint job has a step named mod-tidy that runs "make mod-tidy"
	ciSrc := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	ciLines := strings.Split(ciSrc, "\n")
	foundModTidyStep := false
	foundModTidyRun := false
	for i, line := range ciLines {
		if strings.Contains(line, "- name: mod-tidy") {
			foundModTidyStep = true
			// Look for run: make mod-tidy in subsequent lines
			for j := i + 1; j < len(ciLines) && j < i+10; j++ {
				if strings.Contains(ciLines[j], "run: make mod-tidy") {
					foundModTidyRun = true
					break
				}
			}
			break
		}
	}
	assert.True(t, foundModTidyStep, "ci.yml lint job has no step named mod-tidy; add a step after gofmt that runs make mod-tidy")
	assert.True(t, foundModTidyRun, "ci.yml mod-tidy step does not run 'make mod-tidy'")
}
