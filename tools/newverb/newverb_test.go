package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/scaffold"
)

func TestNewVerbScaffoldValidatesInput(t *testing.T) {
	t.Parallel()

	tree := t.TempDir()
	// No go.mod
	if _, err := scaffold.Verb(tree, "nova-ci", "sample"); err == nil {
		t.Errorf("expected error when go.mod missing, got nil")
	}

	// Create dummy go.mod
	_ = os.WriteFile(filepath.Join(tree, "go.mod"), []byte("module test"), 0o644)
	_ = os.MkdirAll(filepath.Join(tree, "cmd", "nova-ci"), 0o755)
	_ = os.WriteFile(filepath.Join(tree, "cmd", "nova-ci", "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)

	// Invalid tool / verb names
	for _, bad := range []string{"123num", "BadName", "rule with spaces", "for", "type"} {
		if _, err := scaffold.Verb(tree, "nova-ci", bad); err == nil {
			t.Errorf("expected error for invalid verb name %q, got nil", bad)
		}
		if _, err := scaffold.Verb(tree, bad, "probe"); err == nil {
			t.Errorf("expected error for invalid tool name %q, got nil", bad)
		}
	}

	// Valid name
	written, err := scaffold.Verb(tree, "nova-ci", "my-verb")
	if err != nil {
		t.Fatalf("Verb failed on valid name: %v", err)
	}
	if len(written) != 4 {
		t.Fatalf("Verb wrote %d files, want 4", len(written))
	}

	// Second run refused
	if _, err := scaffold.Verb(tree, "nova-ci", "my-verb"); err == nil {
		t.Errorf("expected error on duplicate scaffold, got nil")
	}
}
