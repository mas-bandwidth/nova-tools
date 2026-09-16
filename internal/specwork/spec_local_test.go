package specwork

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSpecLocalDeclaresNotBuilt verifies that docs/SPEC-LOCAL.md states the
// tool is not built: there is no cmd/nova-local binary in the tree, so every
// verb and output line in the spec is a gate for a future build, not a shipped
// promise. The drift audit (2026-09-15) pinned that as a Status line at the top
// of the file, and this test keeps it from silently reading as a shipped tool.
func TestSpecLocalDeclaresNotBuilt(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
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

	data, err := os.ReadFile(filepath.Join(dir, "docs", "SPEC-LOCAL.md"))
	if err != nil {
		t.Fatalf("read SPEC-LOCAL.md: %v", err)
	}
	content := string(data)

	head := content
	if i := strings.Index(head, "\n## "); i >= 0 {
		head = head[:i]
	}
	if !strings.Contains(head, "**not built**") {
		t.Errorf("SPEC-LOCAL.md does not declare the tool not built in its header: %q", strings.SplitN(head, "\n", 2)[0])
	}

	if _, err := os.Stat(filepath.Join(dir, "cmd", "nova-local")); !os.IsNotExist(err) {
		t.Errorf("SPEC-LOCAL.md claims not built, but cmd/nova-local exists")
	}
}
