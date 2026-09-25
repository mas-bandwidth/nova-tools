package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/scaffold"
)

func TestNewRuleScaffoldValidatesInput(t *testing.T) {
	t.Parallel()

	tree := t.TempDir()
	// No go.mod
	if _, err := scaffold.Rule(tree, "sample"); err == nil {
		t.Errorf("expected error when go.mod missing, got nil")
	}

	// Create dummy go.mod
	_ = os.WriteFile(filepath.Join(tree, "go.mod"), []byte("module test"), 0o644)

	// Invalid names
	for _, bad := range []string{"123num", "BadName", "rule with spaces", "for", "type"} {
		if _, err := scaffold.Rule(tree, bad); err == nil {
			t.Errorf("expected error for invalid rule name %q, got nil", bad)
		}
	}

	// Valid name
	written, err := scaffold.Rule(tree, "my-rule")
	if err != nil {
		t.Fatalf("Scaffold failed on valid name: %v", err)
	}
	if len(written) != 3 {
		t.Fatalf("Scaffold wrote %d files, want 3", len(written))
	}

	// Second run refused
	if _, err := scaffold.Rule(tree, "my-rule"); err == nil {
		t.Errorf("expected error on duplicate scaffold, got nil")
	}
}
