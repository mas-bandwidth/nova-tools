package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makefile_pkgs_class_test.go pins the recipe half of security#70 finding 3:
// recipes that run quoted shell scripts (specifically bash -o pipefail -c '...')
// must not interpolate $(PKGS) inside single quotes, because a package name
// containing a single quote would terminate the script and execute in the recipe
// shell. Instead, PKGS is exported to the environment and expanded inside the
// script as an unquoted word list via $$PKGS.

// TestMakefileTestRecipeDoesNotPasteThePackageListIntoItsQuotedScript asserts
// that no recipe line holding `bash -o pipefail -c '` contains the text $(PKGS)
// between its quotes (finding the script by the opening quote and the matching
// closing quote), and that the test target still contains $$PKGS.
func TestMakefileTestRecipeDoesNotPasteThePackageListIntoItsQuotedScript(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	makefile := readFile(t, filepath.Join(root, "Makefile"))
	lines := strings.Split(makefile, "\n")

	const needle = "bash -o pipefail -c '"
	var checked int
	for _, line := range lines {
		idx := strings.Index(line, needle)
		if idx == -1 {
			continue
		}
		checked++
		openQuote := idx + len(needle) - 1
		closeRel := strings.LastIndex(line[openQuote+1:], "'")
		require.NotEqual(t, -1, closeRel, "no matching closing quote on line: %s", line)
		closeQuote := openQuote + 1 + closeRel
		script := line[openQuote+1 : closeQuote]
		assert.NotContainsf(t, script, "$(PKGS)", "recipe line holding bash -o pipefail -c ' pastes $(PKGS) into quoted script: %s", line)
	}
	require.Greater(t, checked, 0, "expected at least one recipe line holding bash -o pipefail -c '")

	var testTargetLines []string
	inTest := false
	for _, line := range lines {
		if strings.HasPrefix(line, "test:") {
			inTest = true
			testTargetLines = append(testTargetLines, line)
			continue
		}
		if inTest {
			if strings.HasPrefix(line, "\t") {
				testTargetLines = append(testTargetLines, line)
			} else if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			} else {
				inTest = false
			}
		}
	}
	require.NotEmpty(t, testTargetLines, "Makefile has no test target lines")
	testTargetText := strings.Join(testTargetLines, "\n")
	assert.Contains(t, testTargetText, "$$PKGS", "Makefile test target must contain $$PKGS")
}
