package sprint

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// ============================================================================
// Stress Test: 10,000 Rapid Mutations & 100% State Hash Parity
// ============================================================================

func TestJournal_ReplayStateHashParity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, "stress_replay.journal")

	const targetMutations = 10000
	const initialEpoch EpochID = 1

	liveCM := NewMemoryCardMachine(initialEpoch)

	writer, err := OpenJournalWriter(journalPath, WithSyncOnAppend(false), WithAutoRepair(true))
	if err != nil {
		t.Fatalf("OpenJournalWriter failed: %v", err)
	}

	// 1. Initialize consumer capacities
	consumers := []ConsumerID{
		"bench:darwin-arm64-1",
		"bench:darwin-arm64-2",
		"bench:linux-x64-1",
		"bench:linux-x64-2",
		"bench:linux-x64-3",
		"bench:studio-m2-1",
		"bench:spacegame-1",
		"bench:hetzner-1",
	}

	mutationCount := 0

	for _, k := range consumers {
		ev := JournalMutationEvent{
			Action:   actSetCapacity,
			Consumer: k,
			Slots:    10000,
			Actor:    "coordinator",
			Epoch:    initialEpoch,
		}
		if err := applyJournalEvent(ctx, liveCM, ev); err != nil {
			t.Fatalf("apply set_capacity failed: %v", err)
		}
		payload, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		if _, err := writer.Append(payload); err != nil {
			t.Fatalf("append failed: %v", err)
		}
		mutationCount++
	}

	streams := []string{"main", "core", "table", "fleet", "auth", "dispatch"}

	// 2. Generate rapid mutations across cards and streams until reaching 10,000
	cardCounter := 0
	writeStartTime := time.Now()

	for mutationCount < targetMutations {
		cardCounter++
		cardID := CardID(fmt.Sprintf("card-%06d", cardCounter))
		stream := streams[cardCounter%len(streams)]
		worker := consumers[cardCounter%len(consumers)]
		reader := consumers[(cardCounter+1)%len(consumers)]

		pathChoice := cardCounter % 4

		// Action: Push
		pushEv := JournalMutationEvent{
			Action: actPush,
			CardID: cardID,
			Stream: stream,
			Actor:  string(worker),
			Epoch:  initialEpoch,
		}
		if err := applyJournalEvent(ctx, liveCM, pushEv); err != nil {
			t.Fatalf("apply push failed: %v", err)
		}
		payload, _ := json.Marshal(pushEv)
		if _, err := writer.Append(payload); err != nil {
			t.Fatalf("append failed: %v", err)
		}
		mutationCount++
		if mutationCount >= targetMutations {
			break
		}

		if pathChoice == 3 {
			// Path 3: Cancel Primary early
			cancelEv := JournalMutationEvent{
				Action: actCancelPrimary,
				CardID: cardID,
				Actor:  "coordinator",
				Reason: "test-cancellation",
				Epoch:  initialEpoch,
			}
			if err := applyJournalEvent(ctx, liveCM, cancelEv); err != nil {
				t.Fatalf("apply cancel failed: %v", err)
			}
			payload, _ = json.Marshal(cancelEv)
			if _, err := writer.Append(payload); err != nil {
				t.Fatalf("append failed: %v", err)
			}
			mutationCount++
			continue
		}

		// Action: Release
		releaseEv := JournalMutationEvent{
			Action: actRelease,
			CardID: cardID,
			Actor:  "coordinator",
			Epoch:  initialEpoch,
		}
		if err := applyJournalEvent(ctx, liveCM, releaseEv); err != nil {
			t.Fatalf("apply release failed: %v", err)
		}
		payload, _ = json.Marshal(releaseEv)
		if _, err := writer.Append(payload); err != nil {
			t.Fatalf("append failed: %v", err)
		}
		mutationCount++
		if mutationCount >= targetMutations {
			break
		}

		// Action: DealWork
		dealWorkEv := JournalMutationEvent{
			Action:   actDealWork,
			CardID:   cardID,
			Consumer: worker,
			Score:    float64(cardCounter),
			Actor:    "coordinator",
			Epoch:    initialEpoch,
		}
		if err := applyJournalEvent(ctx, liveCM, dealWorkEv); err != nil {
			t.Fatalf("apply deal_work failed: %v", err)
		}
		payload, _ = json.Marshal(dealWorkEv)
		if _, err := writer.Append(payload); err != nil {
			t.Fatalf("append failed: %v", err)
		}
		mutationCount++
		if mutationCount >= targetMutations {
			break
		}

		workCopyID := CopyID{Card: cardID, Attempt: 1}

		if pathChoice == 1 {
			// Path 1: Direct EndWorkDone -> done
			doneEv := JournalMutationEvent{
				Action:   actEndWorkDone,
				CopyID:   workCopyID,
				Outcome:  OutcomeOK,
				ToLanded: false,
				Actor:    string(worker),
				Epoch:    initialEpoch,
			}
			if err := applyJournalEvent(ctx, liveCM, doneEv); err != nil {
				t.Fatalf("apply end_work_done failed: %v", err)
			}
			payload, _ = json.Marshal(doneEv)
			if _, err := writer.Append(payload); err != nil {
				t.Fatalf("append failed: %v", err)
			}
			mutationCount++
			continue
		} else if pathChoice == 2 {
			// Path 2: Direct EndWorkDone -> landed (done-already)
			landedEv := JournalMutationEvent{
				Action:   actEndWorkDone,
				CopyID:   workCopyID,
				Outcome:  OutcomeOK,
				ToLanded: true,
				Actor:    string(worker),
				Epoch:    initialEpoch,
			}
			if err := applyJournalEvent(ctx, liveCM, landedEv); err != nil {
				t.Fatalf("apply end_work_done to landed failed: %v", err)
			}
			payload, _ = json.Marshal(landedEv)
			if _, err := writer.Append(payload); err != nil {
				t.Fatalf("append failed: %v", err)
			}
			mutationCount++
			continue
		}

		// Path 0: Full lifecycle: EndWorkPR -> DealRead -> EndReadHigh -> Land
		prEv := JournalMutationEvent{
			Action:    actEndWorkPR,
			CopyID:    workCopyID,
			CommitSHA: fmt.Sprintf("sha-%08d", cardCounter),
			Actor:     string(worker),
			Epoch:     initialEpoch,
		}
		if err := applyJournalEvent(ctx, liveCM, prEv); err != nil {
			t.Fatalf("apply end_work_pr failed: %v", err)
		}
		payload, _ = json.Marshal(prEv)
		if _, err := writer.Append(payload); err != nil {
			t.Fatalf("append failed: %v", err)
		}
		mutationCount++
		if mutationCount >= targetMutations {
			break
		}

		// Action: DealRead
		dealReadEv := JournalMutationEvent{
			Action:   actDealRead,
			CardID:   cardID,
			Consumer: reader,
			Score:    9.0,
			Actor:    "coordinator",
			Epoch:    initialEpoch,
		}
		if err := applyJournalEvent(ctx, liveCM, dealReadEv); err != nil {
			t.Fatalf("apply deal_read failed: %v", err)
		}
		payload, _ = json.Marshal(dealReadEv)
		if _, err := writer.Append(payload); err != nil {
			t.Fatalf("append failed: %v", err)
		}
		mutationCount++
		if mutationCount >= targetMutations {
			break
		}

		readCopyID := CopyID{Card: cardID, Attempt: 2}

		// Action: EndReadHigh
		readHighEv := JournalMutationEvent{
			Action: actEndReadHigh,
			CopyID: readCopyID,
			Score:  9.5,
			Actor:  string(reader),
			Epoch:  initialEpoch,
		}
		if err := applyJournalEvent(ctx, liveCM, readHighEv); err != nil {
			t.Fatalf("apply end_read_high failed: %v", err)
		}
		payload, _ = json.Marshal(readHighEv)
		if _, err := writer.Append(payload); err != nil {
			t.Fatalf("append failed: %v", err)
		}
		mutationCount++
		if mutationCount >= targetMutations {
			break
		}

		// Action: Land
		landEv := JournalMutationEvent{
			Action: actLand,
			CardID: cardID,
			Actor:  "lander",
			Epoch:  initialEpoch,
		}
		if err := applyJournalEvent(ctx, liveCM, landEv); err != nil {
			t.Fatalf("apply land failed: %v", err)
		}
		payload, _ = json.Marshal(landEv)
		if _, err := writer.Append(payload); err != nil {
			t.Fatalf("append failed: %v", err)
		}
		mutationCount++
	}

	// Flush and close writer
	if err := writer.Sync(); err != nil {
		t.Fatalf("writer.Sync failed: %v", err)
	}
	journalFileSize := writer.LastOffset()
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close failed: %v", err)
	}
	writeDuration := time.Since(writeStartTime)

	// 3. Compute Live State Hash
	liveStateHash := liveCM.StateHash()

	// 4. Replay from offset 0 into a completely fresh MemoryCardMachine
	replayedCM := NewMemoryCardMachine(initialEpoch)

	reader, err := OpenJournalReader(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalReader failed: %v", err)
	}
	defer reader.Close()

	replayStartTime := time.Now()
	iter, err := reader.NewIterator()
	if err != nil {
		t.Fatalf("NewIterator failed: %v", err)
	}
	defer iter.Close()

	replayedCount := 0
	var expectedSeq uint64 = 1

	for iter.Next() {
		frame := iter.Frame()
		if frame.Seq != expectedSeq {
			t.Fatalf("replay sequence mismatch: got %d, want %d", frame.Seq, expectedSeq)
		}
		expectedSeq++

		var ev JournalMutationEvent
		if err := json.Unmarshal(frame.Payload, &ev); err != nil {
			t.Fatalf("unmarshal replay frame %d payload failed: %v", frame.Seq, err)
		}

		if err := applyJournalEvent(ctx, replayedCM, ev); err != nil {
			t.Fatalf("replaying event %d (%s) failed: %v", frame.Seq, ev.Action, err)
		}
		replayedCount++
	}

	if err := iter.Err(); err != nil {
		t.Fatalf("iterator stopped with error: %v", err)
	}
	replayDuration := time.Since(replayStartTime)

	if replayedCount != targetMutations {
		t.Fatalf("replayed %d mutations, want %d", replayedCount, targetMutations)
	}

	// 5. Assert 100% Byte-for-Byte State Hash Parity
	replayedStateHash := replayedCM.StateHash()

	if liveStateHash != replayedStateHash {
		t.Fatalf("STATE HASH PARITY FAILURE!\nLive:     %x\nReplayed: %x", liveStateHash, replayedStateHash)
	}

	// 6. Calculate & Report Performance Metrics
	writeMB := float64(journalFileSize) / (1024 * 1024)
	writeOpsSec := float64(targetMutations) / writeDuration.Seconds()
	writeMBSec := writeMB / writeDuration.Seconds()

	replayOpsSec := float64(targetMutations) / replayDuration.Seconds()
	replayMBSec := writeMB / replayDuration.Seconds()

	t.Logf("=== JOURNAL STREAM STRESS & REPLAY PARITY RESULTS ===")
	t.Logf("Total Mutations:       %d ops", targetMutations)
	t.Logf("Journal File Size:     %d bytes (%.2f MB)", journalFileSize, writeMB)
	t.Logf("Average Frame Size:    %.1f bytes", float64(journalFileSize)/float64(targetMutations))
	t.Logf("Live State Hash:       %x", liveStateHash)
	t.Logf("Replayed State Hash:   %x", replayedStateHash)
	t.Logf("State Parity:          100%% EXACT BYTE-FOR-BYTE MATCH")
	t.Logf("-----------------------------------------------------")
	t.Logf("Write Execution Time:  %v", writeDuration)
	t.Logf("Write Throughput:      %.0f ops/sec (%.2f MB/sec)", writeOpsSec, writeMBSec)
	t.Logf("Replay Execution Time: %v", replayDuration)
	t.Logf("Replay Throughput:     %.0f ops/sec (%.2f MB/sec)", replayOpsSec, replayMBSec)
	t.Logf("=====================================================")
}

// ============================================================================
// Micro-Benchmarks for Append and Replay
// ============================================================================

func BenchmarkJournal_AppendStreaming(b *testing.B) {
	tmpDir := b.TempDir()
	journalPath := filepath.Join(tmpDir, "bench_stream.journal")

	writer, err := OpenJournalWriter(journalPath, WithSyncOnAppend(false))
	if err != nil {
		b.Fatalf("OpenJournalWriter failed: %v", err)
	}
	defer writer.Close()

	payload := []byte(`{"act":"push","ep":1,"actor":"bench","card":"card-bench-1","stream":"main"}`)

	b.ResetTimer()
	b.ReportAllocs()
	b.SetBytes(int64(len(payload) + FrameOverhead))

	for i := 0; i < b.N; i++ {
		if _, err := writer.Append(payload); err != nil {
			b.Fatalf("Append failed: %v", err)
		}
	}
}

func BenchmarkJournal_ReplayScan(b *testing.B) {
	tmpDir := b.TempDir()
	journalPath := filepath.Join(tmpDir, "bench_scan.journal")

	writer, err := OpenJournalWriter(journalPath, WithSyncOnAppend(false))
	if err != nil {
		b.Fatalf("OpenJournalWriter failed: %v", err)
	}

	payload := []byte(`{"act":"deal_work","ep":1,"actor":"coord","card":"card-bench-1","consumer":"bench:arm","score":10}`)
	const records = 10000
	for i := 0; i < records; i++ {
		_, _ = writer.Append(payload)
	}
	_ = writer.Sync()
	_ = writer.Close()

	reader, err := OpenJournalReader(journalPath)
	if err != nil {
		b.Fatalf("OpenJournalReader failed: %v", err)
	}
	defer reader.Close()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		frames, err := reader.Scan()
		if err != nil || len(frames) != records {
			b.Fatalf("Scan failed: %v (len %d)", err, len(frames))
		}
	}
}
