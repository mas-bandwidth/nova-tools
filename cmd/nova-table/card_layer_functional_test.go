//go:build functional

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildNovaTable compiles nova-table from source into a private temp directory
// ensuring proven executable provenance for drill tests.
func buildNovaTable(t *testing.T, repoRoot string) string {
	t.Helper()
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "nova-table")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/nova-table")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build nova-table: %v\noutput:\n%s", err, string(out))
	}
	return binPath
}

// runDrillsOnStore executes scripts/card_layer_drills_test.sh against the given redis address.
func runDrillsOnStore(t *testing.T, addr string, binPath string) {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(filepath.Dir(wd))
	testScript := filepath.Join(repoRoot, "scripts", "card_layer_drills_test.sh")

	cmd := exec.Command("sh", testScript, "--redis", addr)
	cmd.Dir = repoRoot
	cleanEnv := []string{}
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "NOVA_SPRINT_REDIS=") && !strings.HasPrefix(env, "NOVA_REDIS_ADDR=") {
			cleanEnv = append(cleanEnv, env)
		}
	}
	cmd.Env = append(cleanEnv,
		"NOVA_SPRINT_REDIS="+addr,
		"NOVA_REDIS_ADDR="+addr,
		"NOVA_TABLE_BIN="+binPath,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("card_layer_drills_test.sh failed on %s: %v\noutput:\n%s", addr, err, string(out))
	}
	t.Logf("output from %s:\n%s", addr, string(out))
}

// TestCardLayerDrills verifies that seed-card-layer.sh, drill-card-lifecycle.sh,
// and drill-card-fault-rejection.sh execute cleanly against a spawned throwaway store.
// Strictly confined to test-owned disposable redis; ambient autodiscovery removed.
func TestCardLayerDrills(t *testing.T) {
	t.Parallel()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(filepath.Dir(wd))
	binPath := buildNovaTable(t, repoRoot)

	t.Run("SpawnedStore", func(t *testing.T) {
		t.Parallel()
		addr := throwaway(t)
		runDrillsOnStore(t, addr, binPath)
	})
}

// runDrillScript runs a drill script against the specified redis address using test-owned paths.
func runDrillScript(t *testing.T, drillName, addr string, binPath string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(filepath.Dir(wd))
	drillScript := filepath.Join(repoRoot, "drills", drillName)
	tmpArtifactDir := t.TempDir()

	cmd := exec.Command("bash", drillScript, "--redis", addr)
	cmd.Dir = repoRoot
	cleanEnv := []string{}
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "NOVA_SPRINT_REDIS=") && !strings.HasPrefix(env, "NOVA_REDIS_ADDR=") {
			cleanEnv = append(cleanEnv, env)
		}
	}
	cmd.Env = append(cleanEnv,
		"NOVA_SPRINT_REDIS="+addr,
		"NOVA_REDIS_ADDR="+addr,
		"NOVA_TABLE_BIN="+binPath,
		"CARD_DRILL_TMPDIR="+tmpArtifactDir,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed on %s: %v\noutput:\n%s", drillName, addr, err, string(out))
	}
	return string(out)
}

// TestDrill1AndDrill2Verification verifies Drill 1 and Drill 2 execution and receipt output.
// Strictly confined to test-owned disposable redis; ambient autodiscovery removed.
func TestDrill1AndDrill2Verification(t *testing.T) {
	t.Parallel()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(filepath.Dir(wd))
	binPath := buildNovaTable(t, repoRoot)

	t.Run("SpawnedStore", func(t *testing.T) {
		t.Parallel()
		addr := throwaway(t)

		// Drill 1: Admission to landed
		out1 := runDrillScript(t, "drill-1-admission-to-landed.sh", addr, binPath)
		for _, want := range []string{
			"RECEIPT op-drill1-admit",
			"RECEIPT op-drill1-resolve-1",
			"RECEIPT op-drill1-landed",
			"Verified committed receipt for op-drill1-admit in Redis",
			"Verified committed receipt for op-drill1-resolve-1 in Redis",
			"Verified committed receipt for op-drill1-landed in Redis",
			"Both distinct reader records verified with disposition=accepted and ci_status=pass",
			"result: PASSED",
		} {
			if !strings.Contains(out1, want) {
				t.Errorf("Drill 1 missing receipt token %q", want)
			}
		}

		// Drill 2: Arrays under real use
		out2 := runDrillScript(t, "drill-2-arrays-under-real-use.sh", addr, binPath)
		for _, want := range []string{
			"RECEIPT op-drill2-admit-10",
			"RECEIPT op-drill2-move-5",
			"RECEIPT op-drill2-replace-06",
			"Verified committed receipt for op-drill2-admit-10 in Redis",
			"Verified committed receipt for op-drill2-move-5 in Redis",
			"Verified committed receipt for op-drill2-replace-06 in Redis",
			"server FCALL runtime refusal: REFUSED REVISION",
			"store image bit-identical to baseline across all 5 refusal controls",
			"result: PASSED",
		} {
			if !strings.Contains(out2, want) {
				t.Errorf("Drill 2 missing receipt token %q", want)
			}
		}
	})
}
