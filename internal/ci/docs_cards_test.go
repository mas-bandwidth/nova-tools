package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissed19Blocked5293CardsIsDone(t *testing.T) {
	t.Parallel()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	secPath := filepath.Join(root, "..", "..", "docs", "security2.md")
	data, err := os.ReadFile(secPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	// security2.md must list TWO security cards, not the coverage card
	if strings.Contains(content, "cov-cmd-nova-sandbox-main") {
		t.Fatal("security2.md must not contain the coverage card (cov-cmd-nova-sandbox-main)")
	}
	// Count security card entries (lines starting with "- security/blocked-5293/")
	lines := strings.Split(content, "\n")
	var count int
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "- security/blocked-5293/") {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("security2.md must list exactly 2 security cards, got %d", count)
	}
	// Verify coverage2.md exists and has the coverage card
	covPath := filepath.Join(root, "..", "..", "docs", "coverage2.md")
	covData, err := os.ReadFile(covPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(covData), "cover-cmd-nova-sandbox-main") {
		t.Fatal("coverage2.md must contain the coverage card")
	}
}
