package sprint

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestNtableTestFilesVetClean verifies that go vet passes on internal/ntable
// files. This ensures the ntable test files don't introduce vet warnings.
func TestNtableTestFilesVetClean(t *testing.T) {
	t.Parallel()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	ntableDir := filepath.Join(wd, "..", "ntable")
	cmd := exec.Command("go", "vet", ntableDir+"/...")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go vet ./internal/ntable/... failed: %v", err)
	}
}
