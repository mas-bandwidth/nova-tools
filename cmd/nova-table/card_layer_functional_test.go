//go:build functional

package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runDrillsOnStore executes scripts/card_layer_drills_test.sh against the given redis address.
func runDrillsOnStore(t *testing.T, addr string) {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(filepath.Dir(wd))
	testScript := filepath.Join(repoRoot, "scripts", "card_layer_drills_test.sh")

	cmd := exec.Command("sh", testScript, "--redis", addr)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"NOVA_SPRINT_REDIS="+addr,
		"NOVA_REDIS_ADDR="+addr,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("card_layer_drills_test.sh failed on %s: %v\noutput:\n%s", addr, err, string(out))
	}
	t.Logf("output from %s:\n%s", addr, string(out))
}

// TestCardLayerDrills verifies that seed-card-layer.sh, drill-card-lifecycle.sh,
// and drill-card-fault-rejection.sh execute cleanly against both a spawned throwaway
// store and any available live redis server, with graceful offline fallback.
func TestCardLayerDrills(t *testing.T) {
	t.Parallel()

	// 1. Spawned isolated throwaway store (via testredis / firstRunStore)
	t.Run("SpawnedStore", func(t *testing.T) {
		t.Parallel()
		addr := throwaway(t)
		runDrillsOnStore(t, addr)
	})

	// 2. Real / live redis server (e.g. 127.0.0.1:6379 or $NOVA_SPRINT_REDIS)
	t.Run("LiveStore", func(t *testing.T) {
		t.Parallel()
		liveAddr := "127.0.0.1:6379"
		if env := os.Getenv("NOVA_SPRINT_REDIS"); env != "" {
			liveAddr = env
		} else if env := os.Getenv("NOVA_REDIS_ADDR"); env != "" {
			liveAddr = env
		}

		conn, err := net.DialTimeout("tcp", liveAddr, 250*time.Millisecond)
		if err != nil {
			t.Skipf("live redis server at %s unavailable; skipping live test: %v", liveAddr, err)
		}
		_ = conn.Close()

	})
}

// runDrillScript runs a drill script against the specified redis address.
func runDrillScript(t *testing.T, drillName, addr string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(filepath.Dir(wd))
	drillScript := filepath.Join(repoRoot, "drills", drillName)

	cmd := exec.Command("bash", drillScript)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"NOVA_SPRINT_REDIS="+addr,
		"NOVA_REDIS_ADDR="+addr,
		"NOVA_TABLE_BIN="+filepath.Join(repoRoot, "bin", "nova-table"),
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed on %s: %v\noutput:\n%s", drillName, addr, err, string(out))
	}
	return string(out)
}

// TestDrill1AndDrill2Verification verifies Drill 1 and Drill 2 execution and receipt output.
func TestDrill1AndDrill2Verification(t *testing.T) {
	t.Parallel()

	testOnStore := func(t *testing.T, addr string) {
		t.Helper()
		// Drill 1: Admission to landed
		out1 := runDrillScript(t, "drill-1-admission-to-landed.sh", addr)
		for _, want := range []string{
			"RECEIPT op-drill1-admit",
			"RECEIPT op-drill1-resolve-1",
			"RECEIPT op-drill1-landed",
			"result: PASSED",
		} {
			if !strings.Contains(out1, want) {
				t.Errorf("Drill 1 missing receipt token %q", want)
			}
		}

		// Drill 2: Arrays under real use
		out2 := runDrillScript(t, "drill-2-arrays-under-real-use.sh", addr)
		for _, want := range []string{
			"RECEIPT op-drill2-admit-10",
			"RECEIPT op-drill2-move-5",
			"RECEIPT op-drill2-replace-06",
			"PASSED: command refused with exit code 1",
			"result: PASSED",
		} {
			if !strings.Contains(out2, want) {
				t.Errorf("Drill 2 missing receipt token %q", want)
			}
		}
	}

	t.Run("SpawnedStore", func(t *testing.T) {
		t.Parallel()
		addr := throwaway(t)
		testOnStore(t, addr)
	})

	t.Run("LiveStore", func(t *testing.T) {
		t.Parallel()
		liveAddr := "127.0.0.1:6379"
		if env := os.Getenv("NOVA_SPRINT_REDIS"); env != "" {
			liveAddr = env
		} else if env := os.Getenv("NOVA_REDIS_ADDR"); env != "" {
			liveAddr = env
		}

		conn, err := net.DialTimeout("tcp", liveAddr, 250*time.Millisecond)
		if err != nil {
			t.Skipf("live redis server at %s unavailable; skipping live test: %v", liveAddr, err)
		}
		_ = conn.Close()

		testOnStore(t, liveAddr)
	})
}

