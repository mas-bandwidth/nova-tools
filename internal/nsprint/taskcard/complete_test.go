package taskcard_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain makes card push's repository probe local for the whole package:
// a bare mirror directory for nova-tools under $NOVA_MIRROR_ROOT, so the lint
// never asks the network. It is set once here, not per test with t.Setenv,
// so every test in the package can run with t.Parallel().
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "taskcard-mirror-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Join(root, "nova-tools.git", "objects"), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Setenv("NOVA_MIRROR_ROOT", root)
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}
