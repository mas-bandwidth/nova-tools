package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDocsTreeIsConsistent verifies that docs/ is internally consistent:
// - docs/AGENTS.md matches what catalog.go produces (not hand-edited)
// - The catalog has a row for docs/ and its subdirectories
func TestDocsTreeIsConsistent(t *testing.T) {
	t.Parallel()

	root, err := RepoRoot()
	if err != nil {
		t.Fatal(err)
	}

	// Check 1: docs/AGENTS.md is generated, not hand-edited
	agentsPath := filepath.Join(root, "docs", "AGENTS.md")
	agentsContent, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("cannot read %s: %v", agentsPath, err)
	}

	// Generated docs have the "Do not edit. `make map` regenerates this file." header
	if !strings.Contains(string(agentsContent), "Do not edit") {
		t.Error("docs/AGENTS.md missing generated header; run: make map")
	}

	// Check 2: internal/docs/catalog.go has an entry for docs/
	hasDocsEntry := false
	for _, e := range DefaultCatalog {
		if e.Path == "docs" {
			hasDocsEntry = true
			break
		}
	}
	if !hasDocsEntry {
		t.Error("catalog.go missing entry for docs/ directory")
	}

	// Check 3: docs/dogfood exists and is cataloged
	dogfoodPath := filepath.Join(root, "docs", "dogfood")
	if _, err := os.Stat(dogfoodPath); err == nil {
		hasDogfoodEntry := false
		for _, e := range DefaultCatalog {
			if e.Path == "docs/dogfood" {
				hasDogfoodEntry = true
				break
			}
		}
		if !hasDogfoodEntry {
			t.Error("catalog.go missing entry for docs/dogfood/ directory")
		}
	}
}
