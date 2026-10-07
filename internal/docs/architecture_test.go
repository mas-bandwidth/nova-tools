package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// architectureTools lists every tool under cmd/ that the release ships.
var architectureTools = []string{
	"nova-bus", "nova-friend", "nova-table", "nova-work", "nova-sprint",
	"nova-redis", "nova-config", "nova-swarm", "nova-secrets", "nova-tokens",
	"nova-memory", "nova-decide", "nova-cairn", "nova-check", "nova-self-talk",
	"nova-fuse", "nova-sandbox", "nova-ci", "nova-version", "nova-update",
	"nova-local", "nova-card", "nova-up", "nova-doctor",
}

// TestArchitecturePageNamesEveryToolAndItsStores verifies docs/ARCHITECTURE.md:
// - every tool directory under cmd/ is named
// - every cited spec file exists
// - every link in the page resolves
func TestArchitecturePageNamesEveryToolAndItsStores(t *testing.T) {
	t.Parallel()

	root := testRoot(t)
	archPath := filepath.Join(root, "docs", "ARCHITECTURE.md")

	// Check that ARCHITECTURE.md exists
	archRaw, err := os.ReadFile(archPath)
	require.NoError(t, err, "cannot read docs/ARCHITECTURE.md")
	arch := string(archRaw)

	// Check that every tool under cmd/ is named in ARCHITECTURE.md
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	require.NoError(t, err)

	// Check that every tool under cmd/ is named in ARCHITECTURE.md
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "nova-") {
			continue
		}
		name := entry.Name()
		assert.Contains(t, arch, name, "tool %s under cmd/ is not named in docs/ARCHITECTURE.md", name)
	}

	// Also verify the expected list matches cmd/
	expected := map[string]bool{}
	for _, tool := range architectureTools {
		expected[tool] = true
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "nova-") {
			continue
		}
		// This check ensures no unexpected tools are in cmd/
		assert.True(t, expected[entry.Name()], "unexpected tool %s in cmd/", entry.Name())
	}

	// Check that every cited spec file exists
	specRe := regexp.MustCompile(`\[([^\]]+)\]\[([^\]]+)\]`)
	for _, m := range specRe.FindAllStringSubmatch(arch, -1) {
		linkName := m[1]
		refName := m[2]
		if strings.HasPrefix(refName, "SPEC-") {
			specPath := filepath.Join(root, "docs", refName+".md")
			_, err := os.Stat(specPath)
			assert.NoError(t, err, "cited spec %s (%s) does not exist", linkName, specPath)
		}
	}

	// Check that links in ARCHITECTURE.md resolve
	// Extract relative links from the page
	linkRe := regexp.MustCompile(`\]\(([^)]+)\)`)
	for _, m := range linkRe.FindAllStringSubmatch(arch, -1) {
		target := m[1]
		if strings.HasPrefix(target, "http") || strings.HasPrefix(target, "#") {
			continue // skip external and anchor links
		}
		// Resolve relative to docs/
		resolved := filepath.Join(root, "docs", target)
		_, err := os.Stat(resolved)
		assert.NoError(t, err, "link %q in docs/ARCHITECTURE.md does not resolve to %s", target, resolved)
	}
}
