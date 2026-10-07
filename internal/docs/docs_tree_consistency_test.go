package docs

import (
	"strings"
	"testing"
)

// TestDocsTreeIsConsistent verifies that the catalog structure is valid and
// self-consistent. It checks that catalog entries have the required fields
// and that paths follow the correct format.
func TestDocsTreeIsConsistent(t *testing.T) {
	t.Parallel()

	// Verify DefaultCatalog is non-empty
	if len(DefaultCatalog) == 0 {
		t.Fatal("DefaultCatalog is empty")
	}

	// Verify catalog entries have required fields
	for _, e := range DefaultCatalog {
		if e.Path == "" {
			t.Error("catalog entry has empty path")
			continue
		}
		if e.Purpose == "" {
			t.Errorf("catalog entry %q has empty purpose", e.Path)
		}
		if e.Guard == "" {
			t.Errorf("catalog entry %q has empty guard", e.Path)
		}
		if e.Command == "" {
			t.Errorf("catalog entry %q has empty command", e.Path)
		}
	}

	// Verify internal/docs is catalogued
	hasDocs := false
	for _, e := range DefaultCatalog {
		if strings.HasSuffix(e.Path, "internal/docs") {
			hasDocs = true
			break
		}
	}
	if !hasDocs {
		t.Fatal("internal/docs should be catalogued")
	}

	// Verify catalog path format is correct (slash-separated, no leading/trailing slash)
	for _, e := range DefaultCatalog {
		if e.Path == "" {
			continue
		}
		if strings.HasPrefix(e.Path, "/") || strings.HasSuffix(e.Path, "/") {
			t.Errorf("catalog path %q has leading or trailing slash", e.Path)
		}
	}
}
