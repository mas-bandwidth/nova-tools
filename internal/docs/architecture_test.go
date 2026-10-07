package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// architecture_test.go holds the docs/ARCHITECTURE.md page against the tree.
// It checks that every tool under cmd/ is named, every cited spec resolves,
// and the core concepts are explained.

var archLinkRe = regexp.MustCompile(`\]\(([^)\s]+)(?:\s[^)]*)?\)`)

// TestArchitecturePageNamesEveryToolAndItsStores verifies docs/ARCHITECTURE.md
// names every tool under cmd/, that every spec link resolves, and that the
// page explains the concepts and architecture.
func TestArchitecturePageNamesEveryToolAndItsStores(t *testing.T) {
	t.Parallel()

	root := testRoot(t)

	archPath := filepath.Join(root, "docs", "ARCHITECTURE.md")
	archRaw, err := os.ReadFile(archPath)
	require.NoError(t, err, "docs/ARCHITECTURE.md must exist")
	arch := string(archRaw)

	// Collect tools under cmd/
	cmdEntries, err := os.ReadDir(filepath.Join(root, "cmd"))
	require.NoError(t, err)

	var tools []string
	for _, e := range cmdEntries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "nova-") {
			tools = append(tools, e.Name())
		}
	}
	sort.Strings(tools)
	require.NotEmpty(t, tools, "no tools under cmd/")

	// Check every tool is named in the architecture page
	for _, tool := range tools {
		assert.Contains(t, arch, tool, "docs/ARCHITECTURE.md must name tool %s", tool)
	}

	// Check every spec link resolves
	inFence := false
	for i, line := range strings.Split(arch, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, m := range archLinkRe.FindAllStringSubmatch(line, -1) {
			written := m[1]
			if !strings.HasPrefix(written, "docs/SPEC-") {
				continue
			}
			resolved := filepath.Join(root, written)
			_, err := os.Stat(resolved)
			assert.NoError(t, err, "docs/ARCHITECTURE.md:%d: spec link %q does not resolve to %s", i+1, written, resolved)
		}
	}

	// Check core concepts are explained
	concepts := []string{"self", "friend", "bud", "card", "sprint", "bus", "seat", "coordinator", "bench", "store", "twin", "loop record"}
	for _, concept := range concepts {
		assert.Contains(t, strings.ToLower(arch), concept, "docs/ARCHITECTURE.md must explain concept %s", concept)
	}

	// Check architecture sections exist
	sections := []string{"Tools Under cmd/", "Stores", "Machines", "Card Path Diagram"}
	for _, section := range sections {
		assert.Contains(t, arch, "### "+section, "docs/ARCHITECTURE.md must have ## %s", section)
	}
}
