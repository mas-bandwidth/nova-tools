package pulse

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// copyTree must write base/go.mod.txt out as go.mod, never leave it as a literal
// go.mod.txt: a nested go.mod under testdata/accept/base/ is a module boundary
// go:embed silently drops (cold read of PR #1730, Emma ebdc9f57d190). Synthetic FS,
// same package as copyTree: no CLI, no embed, no sandbox, deterministic on every bench.
func TestCopyTreeWritesBaseGoModTxtAsGoMod(t *testing.T) {
	fsys := fstest.MapFS{
		"base/go.mod.txt":   &fstest.MapFile{Data: []byte("module fixture\n\ngo 1.21\n")},
		"base/sign/sign.go": &fstest.MapFile{Data: []byte("package sign\n")},
	}
	dst := t.TempDir()
	if err := copyTree(fsys, "base", dst); err != nil {
		t.Fatalf("copyTree: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dst, "go.mod"))
	if err != nil {
		t.Fatalf("go.mod was not written: %v", err)
	}
	if string(raw) != "module fixture\n\ngo 1.21\n" {
		t.Fatalf("go.mod content = %q", raw)
	}
	if _, err := os.Stat(filepath.Join(dst, "go.mod.txt")); err == nil {
		t.Fatal("go.mod.txt was ALSO written verbatim; it must be translated, not duplicated")
	}
}
