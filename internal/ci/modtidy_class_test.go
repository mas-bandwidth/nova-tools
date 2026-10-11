package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAnUntidyModuleIsRefusedOnTheCLPath verifies that the Makefile has a
// mod-tidy target that runs `go mod tidy -diff`, that lint has mod-tidy as a
// prerequisite, and that ci.yml's lint job runs `make mod-tidy`. This guards
// against untidy go.mod files landing on dev and breaking promotion to main.
func TestAnUntidyModuleIsRefusedOnTheCLPath(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

	// Check Makefile has phony mod-tidy target with correct recipe
	mk := parseMakefile(t, filepath.Join(root, "Makefile"))
	_, hasModTidy := mk.recipes["mod-tidy"]
	require.True(t, hasModTidy, "Makefile:%d: no mod-tidy target found; add a mod-tidy target that runs `go mod tidy -diff`")
	require.True(t, mk.phony["mod-tidy"], "Makefile:%d: mod-tidy must be declared in .PHONY")

	recipe := strings.Join(mk.recipeFor("mod-tidy"), "\n")
	assert.Contains(t, recipe, "mod tidy -diff", "Makefile:%d: mod-tidy recipe must run `go mod tidy -diff`: got %q", recipe)

	// Check lint has mod-tidy as a prerequisite
	lintDeps := mk.deps["lint"]
	found := false
	for _, d := range lintDeps {
		if d == "mod-tidy" {
			found = true
			break
		}
	}
	assert.True(t, found, "Makefile:%d: lint target must have mod-tidy as a prerequisite; got deps: %v", lintDeps)

	// Check ci.yml lint job has `make mod-tidy` step
	ciSrc := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	ciCmds := runCommands(ciSrc)

	foundModTidyStep := false
	for _, c := range ciCmds {
		if strings.Contains(c.cmd, "mod-tidy") && strings.Contains(c.cmd, "make ") {
			foundModTidyStep = true
			break
		}
	}
	assert.True(t, foundModTidyStep, "ci.yml:%d: lint job must have a step that runs `make mod-tidy`; add a step named mod-tidy with run: make mod-tidy")
}
