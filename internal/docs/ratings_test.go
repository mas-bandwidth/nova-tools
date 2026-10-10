package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryToolHasACurrentReadRating verifies that every tool in cmd/
// has a 1.2.0 READ rating in docs/ratings/1.2.0/
func TestEveryToolHasACurrentReadRating(t *testing.T) {
	// Find all tools in cmd/
	cmdDir := filepath.Join("..", "..", "cmd")
	tools, err := os.ReadDir(cmdDir)
	if err != nil {
		t.Fatalf("cannot read cmd/: %v", err)
	}

	ratingsBase := filepath.Join("..", "..", "docs", "ratings", "1.2.0")

	// Collect all rating files from rater directories and top level
	ratingFiles := make(map[string]bool)

	// Check top-level files (like nova-bus-alex.md)
	files, err := os.ReadDir(ratingsBase)
	if err == nil {
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			if strings.HasSuffix(f.Name(), ".md") {
				ratingFiles[f.Name()] = true
			}
		}
	}

	// Check rater subdirectories
	raterDirs, err := os.ReadDir(ratingsBase)
	if err == nil {
		for _, entry := range raterDirs {
			if !entry.IsDir() {
				continue
			}
			raterDir := filepath.Join(ratingsBase, entry.Name())
			rls, err := os.ReadDir(raterDir)
			if err != nil {
				continue
			}
			for _, f := range rls {
				if strings.HasSuffix(f.Name(), ".md") {
					ratingFiles[entry.Name()+"/"+f.Name()] = true
				}
			}
		}
	}

	for _, entry := range tools {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "nova-") {
			continue
		}
		tool := entry.Name()

		// Check if any rating file exists for this tool (either at tool name or in rater subdir)
		ratingFound := false
		for path := range ratingFiles {
			if strings.HasPrefix(path, tool+"-") && strings.HasSuffix(path, ".md") {
				ratingFound = true
				break
			}
		}

		if !ratingFound {
			t.Errorf("tool %s lacks a 1.2.0 READ rating in %s", tool, ratingsBase)
		}
	}
}
