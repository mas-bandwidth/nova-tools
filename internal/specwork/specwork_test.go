package specwork

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSpecWorkLocalSpendSingleMention verifies that the fleet spend ceiling
// paragraph (Efficiency lesson 10) names local-spend= exactly once.
// Johnny's lock read 2026-09-15: "under-counts dollars across Studio/Space/mini.
// Name it local-spend= or join the benches. Not two writers."
func TestSpecWorkLocalSpendSingleMention(t *testing.T) {
	// Locate docs/SPEC-WORK.md relative to the repo root.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Walk up until we find go.mod (repo root).
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no go.mod)")
		}
		dir = parent
	}

	data, err := os.ReadFile(filepath.Join(dir, "docs", "SPEC-WORK.md"))
	if err != nil {
		t.Fatalf("read SPEC-WORK.md: %v", err)
	}
	content := string(data)

	// Find the fleet spend ceiling paragraph (item 10).
	start := strings.Index(content, "**Fleet-wide spend ceiling per day.**")
	if start < 0 {
		t.Fatal("could not find fleet spend ceiling paragraph in SPEC-WORK.md")
	}

	// Extract the paragraph: from the start marker to the next blank line or section.
	rest := content[start:]
	end := strings.Index(rest, "\n\n")
	if end < 0 {
		end = len(rest)
	}
	paragraph := rest[:end]

	// Count local-spend= occurrences.
	count := strings.Count(paragraph, "local-spend=")
	if count != 1 {
		t.Errorf("fleet spend ceiling paragraph has local-spend= %d times, want 1", count)
	}
}
