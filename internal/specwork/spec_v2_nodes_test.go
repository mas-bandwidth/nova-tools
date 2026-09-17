package specwork

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSpecWorkV2RecursiveCoordinationNodes verifies that docs/SPEC-WORK.md
// spells out the v2 recursive coordination contract of nova-tools#321: every
// node runs the same accept/perform/delegate lifecycle whatever its human/AI
// composition or depth, a child's completion never closes its parent, the
// coordination tree, the work containment forest and the cross-branch
// reference graph stay distinct, and any size/depth bound refuses visibly
// instead of truncating the tree. The v2 features V2-F01 and V2-F02 of
// ROADMAP.md hang on this section, so it is a gate and not prose.
func TestSpecWorkV2RecursiveCoordinationNodes(t *testing.T) {
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

	data, err := os.ReadFile(filepath.Join(dir, "docs", "SPEC-WORK.md"))
	if err != nil {
		t.Fatalf("read SPEC-WORK.md: %v", err)
	}
	content := string(data)

	start := strings.Index(content, "## Recursive coordination nodes")
	if start < 0 {
		t.Fatal("SPEC-WORK.md has no '## Recursive coordination nodes' section for nova-tools#321")
	}
	section := content[start:]
	if end := strings.Index(section[3:], "\n## "); end >= 0 {
		section = section[:end+3]
	}
	// Collapse the section's markdown line wrapping so the contract phrases
	// are matched as one text, and compare case-insensitively.
	flat := strings.ToLower(strings.Join(strings.Fields(section), " "))

	for _, want := range []string{
		"as above, so below.",
		"composition prescribes no rank.",
		"accepts work from its coordinating parent",
		"passes work down to child nodes",
		"a child's completed task never automatically completes its parent's integrating task.",
		"coordination tree",
		"work containment tree/forest",
		"dependency and reference edges",
		"node, actor, work, operation, attempt and engine/session",
		"refuses or defers visibly",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Recursive coordination nodes section is missing %q", want)
		}
	}
}
