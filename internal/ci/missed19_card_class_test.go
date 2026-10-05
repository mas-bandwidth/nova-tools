package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The record for missed-19: cards blocked on #5293 are now tracked.
// This test asserts that the docs record exists and carries the right claim.

func TestMissed19Blocked5293CardsIsDone(t *testing.T) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	const path = "docs/missed-19-blocked-5293-cards.md"
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatalf("missing docs record: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "# Missed 19:") {
		t.Fatal("docs record missing title header")
	}
	if !strings.Contains(s, "#5293") {
		t.Fatal("docs record missing #5293 mention")
	}
	if !strings.Contains(s, "security2") || !strings.Contains(s, "coverage2") {
		t.Fatal("docs record missing security2/coverage2 streams")
	}
}
