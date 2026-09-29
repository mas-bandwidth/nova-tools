//go:build functional

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCardLayerDrills verifies that seed-card-layer.sh, drill-card-lifecycle.sh,
// and drill-card-fault-rejection.sh execute cleanly against the functional test store.
func TestCardLayerDrills(t *testing.T) {
	t.Parallel()

	addr := throwaway(t)

	// Locate repository root from current test file
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(filepath.Dir(wd))
	testScript := filepath.Join(repoRoot, "scripts", "card_layer_drills_test.sh")

	cmd := exec.Command("sh", testScript)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"NOVA_SPRINT_REDIS="+addr,
		"NOVA_REDIS_ADDR="+addr,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("card_layer_drills_test.sh failed: %v\noutput:\n%s", err, string(out))
	}
	t.Logf("card layer test suite output:\n%s", string(out))
}
