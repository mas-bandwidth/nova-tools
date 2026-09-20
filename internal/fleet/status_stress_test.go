package fleet

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// setupStressFleetTopology constructs an on-disk simulated fleet topology
// with numNodes nodes and slotsPerNode active slots per node.
// Total active slots = numNodes * slotsPerNode.
func setupStressFleetTopology(tb testing.TB, numNodes, slotsPerNode int) (string, []QueryNodeOptions, func(int) bool) {
	tb.Helper()

	rootDir := tb.TempDir()
	var nodeOpts []QueryNodeOptions

	alivePids := make(map[int]bool)
	baseDiskSHA := "86abcf23dd6cd95668ae1a865e11e29556996b03"
	driftDiskSHA := "0000000000000000000000000000000000000000"

	pidCounter := 20000

	for i := 1; i <= numNodes; i++ {
		nodeName := fmt.Sprintf("node-%02d", i)
		nodeRoot := filepath.Join(rootDir, nodeName)

		// Alternate commit drift: every 4th node runs an older drifted commit
		nodeDiskSHA := baseDiskSHA
		runningSHA := baseDiskSHA
		if i%4 == 0 {
			runningSHA = driftDiskSHA
		}

		for s := 1; s <= slotsPerNode; s++ {
			slotName := fmt.Sprintf("slot-%d", s)
			slotDir := filepath.Join(nodeRoot, "slots", slotName)
			jobName := fmt.Sprintf("card-job-%02d-%d", i, s)
			jobDir := filepath.Join(slotDir, "jobs", jobName)

			pidCounter++
			alivePids[pidCounter] = true

			rec := LaunchRecord{
				AttemptID:        MustMintAttemptID(),
				Timestamp:        time.Now().UTC().Add(-time.Duration(s*5) * time.Minute),
				CardHash:         fmt.Sprintf("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852%04x", (i*100)+s),
				Lane:             "from-emma",
				Slot:             slotName,
				Job:              jobName,
				Node:             nodeName,
				RunningCommitSHA: runningSHA,
				Pid:              pidCounter,
				State:            "STARTED",
			}

			if err := RecordLaunch(slotDir, jobDir, rec); err != nil {
				tb.Fatalf("setup stress slot %s/%s: %v", nodeName, slotName, err)
			}
		}

		nodeOpts = append(nodeOpts, QueryNodeOptions{
			NodeName: nodeName,
			NodeRoot: nodeRoot,
			DiskSHA:  nodeDiskSHA,
		})
	}

	isAlive := func(pid int) bool {
		return alivePids[pid]
	}

	for i := range nodeOpts {
		nodeOpts[i].IsAlive = isAlive
	}

	return rootDir, nodeOpts, isAlive
}

// TestFleetStatus_StressTopology32Nodes128Slots proves:
// 1. Simulates a fleet with 32 nodes and 128 active slots (4 slots/node).
// 2. Measures time to query per-slot commit SHAs and serialize fleet status.
// 3. Proves memory usage remains bounded (< 50MB) and latency < 100ms.
func TestFleetStatus_StressTopology32Nodes128Slots(t *testing.T) {
	const numNodes = 32
	const slotsPerNode = 4
	const totalActiveSlots = numNodes * slotsPerNode // 128 active slots

	t.Logf("Setting up simulated fleet: %d nodes, %d slots/node = %d active slots...",
		numNodes, slotsPerNode, totalActiveSlots)

	setupStart := time.Now()
	_, nodeOpts, _ := setupStressFleetTopology(t, numNodes, slotsPerNode)
	t.Logf("Setup completed in %v", time.Since(setupStart))

	// Force GC before measuring memory delta
	runtime.GC()
	var mBefore runtime.MemStats
	runtime.ReadMemStats(&mBefore)

	// Warmup/First run vs subsequent runs
	t.Log("Testing QueryFleetStatus across multiple iterations...")
	for iter := 1; iter <= 5; iter++ {
		iterStart := time.Now()
		rep, err := QueryFleetStatus(nodeOpts)
		if err != nil {
			t.Fatalf("QueryFleetStatus failed on iter %d: %v", iter, err)
		}
		t.Logf("  Iteration %d: %v (slots: %d)", iter, time.Since(iterStart), rep.Summary.LiveSlots)
	}

	// Measure Query + SHA Inspection + Serialization
	queryStart := time.Now()

	// 1. Query fleet status across all 32 nodes and 128 active slots
	report, err := QueryFleetStatus(nodeOpts)
	if err != nil {
		t.Fatalf("QueryFleetStatus failed: %v", err)
	}
	queryDuration := time.Since(queryStart)

	// 2. Query and verify per-slot commit SHAs
	shaInspectionStart := time.Now()
	slotCount := 0
	driftCount := 0
	matchCount := 0

	for _, node := range report.Nodes {
		for _, slot := range node.Slots {
			slotCount++
			if slot.LiveStatus != LiveStatusRunning {
				t.Errorf("expected slot %s on node %s to be RUNNING, got %s",
					slot.Slot, node.Name, slot.LiveStatus)
			}
			if slot.RunningCommitSHA == "" {
				t.Errorf("slot %s on node %s has empty RunningCommitSHA", slot.Slot, node.Name)
			}
			if slot.DiskCommitSHA == "" {
				t.Errorf("slot %s on node %s has empty DiskCommitSHA", slot.Slot, node.Name)
			}
			if slot.SHAMatch {
				matchCount++
			} else {
				driftCount++
			}
		}
	}
	shaInspectionDuration := time.Since(shaInspectionStart)

	if slotCount != totalActiveSlots {
		t.Fatalf("expected %d slots, queried %d", totalActiveSlots, slotCount)
	}
	if len(report.Nodes) != numNodes {
		t.Fatalf("expected %d nodes, got %d", numNodes, len(report.Nodes))
	}
	if report.Summary.LiveSlots != totalActiveSlots {
		t.Fatalf("expected %d live slots in summary, got %d", totalActiveSlots, report.Summary.LiveSlots)
	}

	// 3. Measure time to serialize fleet status (JSON, JSON Indented, Text)
	serializeStart := time.Now()
	rawJSON, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	marshalJSONDuration := time.Since(serializeStart)

	indentStart := time.Now()
	indentJSON, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent failed: %v", err)
	}
	marshalIndentDuration := time.Since(indentStart)

	textStart := time.Now()
	textFormat := FormatFleetStatus(report)
	formatTextDuration := time.Since(textStart)

	totalLatency := time.Since(queryStart)

	// 4. Memory measurements
	var mAfter runtime.MemStats
	runtime.ReadMemStats(&mAfter)

	totalAllocDelta := mAfter.TotalAlloc - mBefore.TotalAlloc
	heapAllocDelta := int64(mAfter.HeapAlloc) - int64(mBefore.HeapAlloc)

	// Verify unmarshaling to ensure complete serialization roundtrip
	var roundtrip FleetStatusReport
	if err := json.Unmarshal(rawJSON, &roundtrip); err != nil {
		t.Fatalf("json roundtrip unmarshal failed: %v", err)
	}
	if roundtrip.Summary.LiveSlots != totalActiveSlots {
		t.Fatalf("roundtrip summary mismatch: %d vs %d", roundtrip.Summary.LiveSlots, totalActiveSlots)
	}

	t.Logf("=== FLEET STATUS STRESS TEST RESULTS (%d Nodes, %d Active Slots) ===", numNodes, totalActiveSlots)
	t.Logf("  Query Fleet Status:       %v", queryDuration)
	t.Logf("  Per-slot SHA Inspection:  %v", shaInspectionDuration)
	t.Logf("  Serialize JSON (compact): %v (size: %d bytes)", marshalJSONDuration, len(rawJSON))
	t.Logf("  Serialize JSON (indent):  %v (size: %d bytes)", marshalIndentDuration, len(indentJSON))
	t.Logf("  Serialize Text Format:    %v (size: %d bytes)", formatTextDuration, len(textFormat))
	t.Logf("  Total End-to-End Latency: %v", totalLatency)
	t.Logf("  Total Memory Allocated:   %d bytes (%.2f MB)", totalAllocDelta, float64(totalAllocDelta)/(1024*1024))
	t.Logf("  Heap In-Use Delta:        %d bytes (%.2f MB)", heapAllocDelta, float64(heapAllocDelta)/(1024*1024))
	t.Logf("  Slot Distribution:        %d active, %d matching SHA, %d drifted SHA", slotCount, matchCount, driftCount)

	// Invariant Checks:
	// Latency MUST be < 100ms
	const maxAllowedLatency = 100 * time.Millisecond
	if totalLatency >= maxAllowedLatency {
		t.Errorf("LATENCY VIOLATION: total latency %v exceeds threshold of %v", totalLatency, maxAllowedLatency)
	}

	// Memory MUST be < 50MB
	const maxAllowedMemoryBytes = 50 * 1024 * 1024 // 50MB
	if totalAllocDelta >= maxAllowedMemoryBytes {
		t.Errorf("MEMORY BOUND VIOLATION: total allocated %d bytes (%.2f MB) exceeds threshold of 50MB",
			totalAllocDelta, float64(totalAllocDelta)/(1024*1024))
	}
}

// BenchmarkFleetStatus_Query_32Nodes128Slots benchmarks querying the 32-node, 128-slot fleet topology.
func BenchmarkFleetStatus_Query_32Nodes128Slots(b *testing.B) {
	_, nodeOpts, _ := setupStressFleetTopology(b, 32, 4)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		report, err := QueryFleetStatus(nodeOpts)
		if err != nil {
			b.Fatalf("query failed: %v", err)
		}
		if report.Summary.LiveSlots != 128 {
			b.Fatalf("unexpected live slots: %d", report.Summary.LiveSlots)
		}
	}
}

// BenchmarkFleetStatus_QueryAndPerSlotSHA_32Nodes128Slots benchmarks query plus per-slot SHA verification.
func BenchmarkFleetStatus_QueryAndPerSlotSHA_32Nodes128Slots(b *testing.B) {
	_, nodeOpts, _ := setupStressFleetTopology(b, 32, 4)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		report, err := QueryFleetStatus(nodeOpts)
		if err != nil {
			b.Fatalf("query failed: %v", err)
		}
		matched := 0
		for _, n := range report.Nodes {
			for _, s := range n.Slots {
				if s.SHAMatch {
					matched++
				}
			}
		}
		if matched == 0 {
			b.Fatal("expected matching SHAs")
		}
	}
}

// BenchmarkFleetStatus_SerializeJSON_32Nodes128Slots benchmarks JSON serialization of 32-node report.
func BenchmarkFleetStatus_SerializeJSON_32Nodes128Slots(b *testing.B) {
	_, nodeOpts, _ := setupStressFleetTopology(b, 32, 4)
	report, err := QueryFleetStatus(nodeOpts)
	if err != nil {
		b.Fatalf("setup query failed: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		data, err := json.Marshal(report)
		if err != nil {
			b.Fatalf("json marshal failed: %v", err)
		}
		if len(data) == 0 {
			b.Fatal("empty json output")
		}
	}
}

// BenchmarkFleetStatus_SerializeText_32Nodes128Slots benchmarks text serialization of 32-node report.
func BenchmarkFleetStatus_SerializeText_32Nodes128Slots(b *testing.B) {
	_, nodeOpts, _ := setupStressFleetTopology(b, 32, 4)
	report, err := QueryFleetStatus(nodeOpts)
	if err != nil {
		b.Fatalf("setup query failed: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		out := FormatFleetStatus(report)
		if len(out) == 0 {
			b.Fatal("empty formatted text output")
		}
	}
}

// BenchmarkFleetStatus_EndToEnd_32Nodes128Slots benchmarks full query + SHA check + JSON serialization.
func BenchmarkFleetStatus_EndToEnd_32Nodes128Slots(b *testing.B) {
	_, nodeOpts, _ := setupStressFleetTopology(b, 32, 4)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		report, err := QueryFleetStatus(nodeOpts)
		if err != nil {
			b.Fatalf("query failed: %v", err)
		}
		// Per-slot SHA check
		for _, n := range report.Nodes {
			for _, s := range n.Slots {
				_ = s.RunningCommitSHA
				_ = s.SHAMatch
			}
		}
		// Serialization
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			b.Fatalf("json.MarshalIndent failed: %v", err)
		}
		if len(data) == 0 {
			b.Fatal("empty json data")
		}
	}
}
