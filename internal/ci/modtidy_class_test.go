package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// modtidy_class_test.go verifies the mod-tidy gate for go.mod tidiness.
// It checks that the Makefile has a mod-tidy target, that lint depends on it,
// and that ci.yml's lint step runs make mod-tidy.

// TestAnUntidyModuleIsRefusedOnTheCLPath reads the repository's own files to
// verify the mod-tidy gate is in place. It fails unless:
// 1. The Makefile has a phony mod-tidy target whose recipe runs "mod tidy -diff"
// 2. The lint target names mod-tidy as a prerequisite
// 3. The lint job of .github/workflows/ci.yml has a run line that is exactly "make mod-tidy"
func TestAnUntidyModuleIsRefusedOnTheCLPath(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	makefilePath := filepath.Join(root, "Makefile")
	ciPath := filepath.Join(root, ".github", "workflows", "ci.yml")

	mk := parseMakefile(t, makefilePath)

	// 1. Check mod-tidy target exists and runs "mod tidy -diff"
	recipe, ok := mk.recipes["mod-tidy"]
	require.True(t, ok, "Makefile declares no mod-tidy target; add it as a .PHONY target with recipe: $(GO) mod tidy -diff")
	require.True(t, mk.phony["mod-tidy"], "Makefile .PHONY does not name mod-tidy; add it to .PHONY")
	found := false
	for _, line := range recipe {
		if strings.Contains(line, "mod tidy -diff") {
			found = true
			break
		}
	}
	require.True(t, found, "Makefile mod-tidy recipe does not run 'mod tidy -diff'; found: %v", recipe)

	// 2. Check lint targets mod-tidy as a prerequisite
	lintDeps := mk.deps["lint"]
	found = false
	for _, dep := range lintDeps {
		if dep == "mod-tidy" {
			found = true
			break
		}
	}
	require.True(t, found, "Makefile lint target does not name mod-tidy as a prerequisite; add mod-tidy to the lint line")

	// 3. Check ci.yml's lint job has a step that runs "make mod-tidy"
	ciSrc := readFile(t, ciPath)
	cmds := runCommands(ciSrc)
	found = false
	for _, c := range cmds {
		if c.cmd == "make mod-tidy" {
			found = true
			break
		}
	}
	require.True(t, found, ".github/workflows/ci.yml lint job has no step that runs 'make mod-tidy'; add a step: name mod-tidy, run make mod-tidy")
}
