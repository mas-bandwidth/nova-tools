package descendant

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDescendantProbe(t *testing.T) {
	dir := os.Getenv("NOVA_CI_FLAKE_PROBE_DIR")
	if dir == "" {
		t.Fatal("missing NOVA_CI_FLAKE_PROBE_DIR environment variable")
	}
	readyFile := filepath.Join(dir, "descendant.ready")

	cmd := exec.Command("/bin/sh", "-c", `echo $$; exec sleep 60`)
	r, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	n, err := r.Read(buf)
	if err != nil || n == 0 {
		t.Fatalf("failed to read child pid: %v", err)
	}
	if err := os.WriteFile(readyFile, buf[:n], 0o600); err != nil {
		t.Fatalf("failed to write ready file: %v", err)
	}
	_ = r.Close()
}
