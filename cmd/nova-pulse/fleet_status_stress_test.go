package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

// setupStressPulseFleet constructs on-disk fleet files with 32 machines and 128 active slots.
func setupStressPulseFleet(tb testing.TB, numNodes, slotsPerNode int) (string, string) {
	tb.Helper()

	rootDir := tb.TempDir()
	machinesFile := filepath.Join(rootDir, "machines.tsv")

	var machinesContent strings.Builder
	machinesContent.WriteString("# Simulated Fleet Topology for Stress Testing\n")

	baseSHA := "86abcf23dd6cd95668ae1a865e11e29556996b03"
	driftSHA := "0000000000000000000000000000000000000000"

	for i := 1; i <= numNodes; i++ {
		nodeName := fmt.Sprintf("node-%02d", i)
		nodeDir := filepath.Join(rootDir, nodeName)

		// machines.tsv line: name, ssh, os/arch, roles, seat, cores, notes
		fmt.Fprintf(&machinesContent, "%s\t%s.internal\tdarwin/arm64\tbench\t-\t16\tsimulated stress node\n", nodeName, nodeName)

		runningSHA := baseSHA
		if i%4 == 0 {
			runningSHA = driftSHA
		}

		for s := 1; s <= slotsPerNode; s++ {
			slotName := fmt.Sprintf("slot-%d", s)
			slotDir := filepath.Join(nodeDir, "slots", slotName)
			jobName := fmt.Sprintf("card-%02d-%d", i, s)
			jobDir := filepath.Join(slotDir, "jobs", jobName)

			rec := fleet.LaunchRecord{
				AttemptID:        fleet.MustMintAttemptID(),
				Timestamp:        time.Now().UTC().Add(-time.Duration(s) * time.Minute),
				CardHash:         fmt.Sprintf("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852%04x", (i*100)+s),
				Lane:             "from-emma",
				Slot:             slotName,
				Job:              jobName,
				Node:             nodeName,
				RunningCommitSHA: runningSHA,
				Pid:              os.Getpid(), // Use caller's own PID so isAlive() returns true!
				State:            "STARTED",
			}

			if err := fleet.RecordLaunch(slotDir, jobDir, rec); err != nil {
				tb.Fatalf("record launch %s/%s: %v", nodeName, slotName, err)
			}
		}
	}

	if err := os.WriteFile(machinesFile, []byte(machinesContent.String()), 0o644); err != nil {
		tb.Fatalf("write machines.tsv: %v", err)
	}

	return rootDir, machinesFile
}

// TestCmdFleetStatus_StressTopology32Nodes128Slots verifies `nova-pulse fleet status` CLI performance:
// 1. Simulates 32 nodes and 128 active slots.
// 2. Invokes CLI with --machines and --root and --json.
// 3. Proves memory usage < 50MB and latency < 100ms.
func TestCmdFleetStatus_StressTopology32Nodes128Slots(t *testing.T) {
	const numNodes = 32
	const slotsPerNode = 4
	const totalActiveSlots = numNodes * slotsPerNode // 128 active slots

	t.Logf("Setting up simulated fleet for CLI stress test: %d nodes, %d slots...", numNodes, totalActiveSlots)
	rootDir, machinesFile := setupStressPulseFleet(t, numNodes, slotsPerNode)

	// Warmup execution
	var warmupOut, warmupErr bytes.Buffer
	rc := cmdFleet([]string{"status", "--machines", machinesFile, "--root", rootDir, "--json"}, &warmupOut, &warmupErr)
	if rc != 0 {
		t.Fatalf("warmup cmdFleet status failed (rc=%d): %s", rc, warmupErr.String())
	}

	// Memory & latency profiling
	runtime.GC()
	var mBefore runtime.MemStats
	runtime.ReadMemStats(&mBefore)

	start := time.Now()
	var stdout, stderr bytes.Buffer
	rc = cmdFleet([]string{"status", "--machines", machinesFile, "--root", rootDir, "--json"}, &stdout, &stderr)
	elapsed := time.Since(start)

	var mAfter runtime.MemStats
	runtime.ReadMemStats(&mAfter)

	totalAllocDelta := mAfter.TotalAlloc - mBefore.TotalAlloc

	if rc != 0 {
		t.Fatalf("cmdFleet status failed (rc=%d): %s", rc, stderr.String())
	}

	// Verify report contents
	var report fleet.FleetStatusReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("json unmarshal failed: %v", err)
	}

	if len(report.Nodes) != numNodes {
		t.Fatalf("expected %d nodes in report, got %d", numNodes, len(report.Nodes))
	}
	if report.Summary.LiveSlots != totalActiveSlots {
		t.Fatalf("expected %d live slots, got %d", totalActiveSlots, report.Summary.LiveSlots)
	}

	t.Logf("=== NOVA-PULSE FLEET STATUS CLI STRESS TEST (32 Nodes, 128 Slots) ===")
	t.Logf("  CLI Command Latency:  %v", elapsed)
	t.Logf("  Total Memory Delta:   %d bytes (%.2f MB)", totalAllocDelta, float64(totalAllocDelta)/(1024*1024))
	t.Logf("  JSON Payload Size:    %d bytes", stdout.Len())
	t.Logf("  Live Slots Reported:  %d", report.Summary.LiveSlots)
	t.Logf("  Drift Slots Reported: %d", report.Summary.DriftCount)

	// Invariant checks
	const maxAllowedLatency = 100 * time.Millisecond
	if elapsed >= maxAllowedLatency {
		t.Errorf("LATENCY VIOLATION: CLI latency %v exceeds threshold %v", elapsed, maxAllowedLatency)
	}

	const maxAllowedMemoryBytes = 50 * 1024 * 1024
	if totalAllocDelta >= maxAllowedMemoryBytes {
		t.Errorf("MEMORY VIOLATION: CLI allocated %d bytes exceeds 50MB", totalAllocDelta)
	}
}

// BenchmarkCmdFleetStatus_JSON_32Nodes128Slots benchmarks the end-to-end CLI command with JSON output.
func BenchmarkCmdFleetStatus_JSON_32Nodes128Slots(b *testing.B) {
	rootDir, machinesFile := setupStressPulseFleet(b, 32, 4)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		var stdout, stderr bytes.Buffer
		rc := cmdFleet([]string{"status", "--machines", machinesFile, "--root", rootDir, "--json"}, &stdout, &stderr)
		if rc != 0 {
			b.Fatalf("cmdFleet failed: %s", stderr.String())
		}
	}
}
