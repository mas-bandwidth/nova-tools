package sprint

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// ============================================================================
// Section 1: Round-Trip Write & Read Tests
// ============================================================================

func TestJournal_RoundTripWriteRead(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, "test.journal")

	writer, err := OpenJournalWriter(journalPath, WithSyncOnAppend(true))
	if err != nil {
		t.Fatalf("OpenJournalWriter failed: %v", err)
	}
	defer writer.Close()

	testPayloads := [][]byte{
		{},                                // Empty payload
		[]byte("hello world"),             // Small ASCII
		[]byte("🎉 Unicode test 🚀"),        // UTF-8 text
		bytes.Repeat([]byte{0xAB}, 1024),  // 1 KiB binary
		bytes.Repeat([]byte{0x55}, 65536), // 64 KiB binary
	}

	writtenFrames := make([]*Frame, len(testPayloads))
	for i, p := range testPayloads {
		ts := time.Now().Add(time.Duration(i) * time.Millisecond)
		frame, err := writer.AppendWithTimestamp(p, ts)
		if err != nil {
			t.Fatalf("Append payload %d failed: %v", i, err)
		}
		if frame.Seq != uint64(i+1) {
			t.Errorf("frame.Seq = %d, want %d", frame.Seq, i+1)
		}
		if frame.PayloadLen != uint32(len(p)) {
			t.Errorf("frame.PayloadLen = %d, want %d", frame.PayloadLen, len(p))
		}
		if !bytes.Equal(frame.Payload, p) {
			t.Errorf("frame.Payload mismatch for index %d", i)
		}
		writtenFrames[i] = frame
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close failed: %v", err)
	}

	// Read back via JournalReader
	reader, err := OpenJournalReader(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalReader failed: %v", err)
	}
	defer reader.Close()

	readFrames, err := reader.Scan()
	if err != nil {
		t.Fatalf("reader.Scan failed: %v", err)
	}

	if len(readFrames) != len(testPayloads) {
		t.Fatalf("read %d frames, want %d", len(readFrames), len(testPayloads))
	}

	for i, rf := range readFrames {
		wf := writtenFrames[i]
		if rf.Seq != wf.Seq {
			t.Errorf("[%d] Seq mismatch: got %d, want %d", i, rf.Seq, wf.Seq)
		}
		if rf.Timestamp.UnixNano() != wf.Timestamp.UnixNano() {
			t.Errorf("[%d] Timestamp mismatch: got %v, want %v", i, rf.Timestamp, wf.Timestamp)
		}
		if rf.PayloadLen != wf.PayloadLen {
			t.Errorf("[%d] PayloadLen mismatch: got %d, want %d", i, rf.PayloadLen, wf.PayloadLen)
		}
		if !bytes.Equal(rf.Payload, wf.Payload) {
			t.Errorf("[%d] Payload content mismatch", i)
		}
		if rf.CRC32 != wf.CRC32 {
			t.Errorf("[%d] CRC32 mismatch: got %08x, want %08x", i, rf.CRC32, wf.CRC32)
		}
		if rf.SHA256 != wf.SHA256 {
			t.Errorf("[%d] SHA256 mismatch: got %x, want %x", i, rf.SHA256, wf.SHA256)
		}
		if rf.Offset != wf.Offset {
			t.Errorf("[%d] Offset mismatch: got %d, want %d", i, rf.Offset, wf.Offset)
		}
		if rf.TotalSize != wf.TotalSize {
			t.Errorf("[%d] TotalSize mismatch: got %d, want %d", i, rf.TotalSize, wf.TotalSize)
		}
	}
}

// ============================================================================
// Section 2: Replay Order & Iterator Tests
// ============================================================================

func TestJournal_ReplayOrder(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, "replay.journal")

	writer, err := OpenJournalWriter(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalWriter failed: %v", err)
	}

	const numRecords = 100
	for i := 1; i <= numRecords; i++ {
		payload := []byte(fmt.Sprintf("record-sequence-%d", i))
		if _, err := writer.Append(payload); err != nil {
			t.Fatalf("Append %d failed: %v", i, err)
		}
	}
	writer.Close()

	reader, err := OpenJournalReader(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalReader failed: %v", err)
	}
	defer reader.Close()

	iter, err := reader.NewIterator()
	if err != nil {
		t.Fatalf("NewIterator failed: %v", err)
	}
	defer iter.Close()

	var expectedSeq uint64 = 1
	var expectedOffset int64 = 0

	for iter.Next() {
		frame := iter.Frame()
		if frame.Seq != expectedSeq {
			t.Fatalf("iteration expected seq %d, got %d", expectedSeq, frame.Seq)
		}
		if frame.Offset != expectedOffset {
			t.Fatalf("iteration expected offset %d, got %d", expectedOffset, frame.Offset)
		}
		expectedOffset += frame.TotalSize
		expectedSeq++
	}

	if err := iter.Err(); err != nil {
		t.Fatalf("iterator stopped with unexpected error: %v", err)
	}

	if expectedSeq != numRecords+1 {
		t.Fatalf("replayed %d records, want %d", expectedSeq-1, numRecords)
	}
}

func TestJournal_IteratorFromOffset(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, "seek_replay.journal")

	writer, err := OpenJournalWriter(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalWriter failed: %v", err)
	}

	var targetOffset int64
	for i := 1; i <= 10; i++ {
		frame, err := writer.Append([]byte(fmt.Sprintf("msg-%d", i)))
		if err != nil {
			t.Fatalf("Append failed: %v", err)
		}
		if i == 6 {
			targetOffset = frame.Offset
		}
	}
	writer.Close()

	reader, err := OpenJournalReader(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalReader failed: %v", err)
	}
	defer reader.Close()

	iter, err := reader.NewIteratorAt(targetOffset)
	if err != nil {
		t.Fatalf("NewIteratorAt failed: %v", err)
	}
	defer iter.Close()

	var seqs []uint64
	for iter.Next() {
		seqs = append(seqs, iter.Frame().Seq)
	}

	if err := iter.Err(); err != nil {
		t.Fatalf("iterator error: %v", err)
	}

	if len(seqs) != 5 {
		t.Fatalf("got %d frames from offset, want 5", len(seqs))
	}
	if seqs[0] != 6 || seqs[4] != 10 {
		t.Fatalf("unexpected seq range: %v", seqs)
	}
}

// ============================================================================
// Section 3: Checksum & Corruption Detection Tests
// ============================================================================

func TestJournal_CorruptFrameDetection_CRC32(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, "corrupt_crc.journal")

	writer, err := OpenJournalWriter(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalWriter failed: %v", err)
	}

	frame, err := writer.Append([]byte("critical-unaltered-payload"))
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	writer.Close()

	// Corrupt one byte in the payload area
	corruptOffset := frame.Offset + HeaderSize + 3
	data, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	data[corruptOffset] ^= 0xFF // Flip bits
	if err := os.WriteFile(journalPath, data, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	reader, err := OpenJournalReader(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalReader failed: %v", err)
	}
	defer reader.Close()

	_, err = reader.Scan()
	if err == nil {
		t.Fatalf("expected CRC error on corrupted payload byte, got nil")
	}
	if !errors.Is(err, ErrCorruptCRC) {
		t.Fatalf("expected ErrCorruptCRC, got: %v", err)
	}
}

func TestJournal_CorruptFrameDetection_SHA256(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, "corrupt_sha.journal")

	writer, err := OpenJournalWriter(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalWriter failed: %v", err)
	}

	frame, err := writer.Append([]byte("sha-verification-payload"))
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	writer.Close()

	// Corrupt a byte in the SHA256 trailer without altering header, payload, or CRC32
	shaOffset := frame.Offset + HeaderSize + int64(frame.PayloadLen) + 4 + 7
	data, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	data[shaOffset] ^= 0xAA // Flip bits in SHA256 hash
	if err := os.WriteFile(journalPath, data, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	reader, err := OpenJournalReader(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalReader failed: %v", err)
	}
	defer reader.Close()

	_, err = reader.Scan()
	if err == nil {
		t.Fatalf("expected SHA256 error on corrupted hash bytes, got nil")
	}
	if !errors.Is(err, ErrCorruptSHA256) {
		t.Fatalf("expected ErrCorruptSHA256, got: %v", err)
	}
}

func TestJournal_InvalidMagic(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, "bad_magic.journal")

	writer, err := OpenJournalWriter(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalWriter failed: %v", err)
	}
	if _, err := writer.Append([]byte("sample")); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	writer.Close()

	// Corrupt magic bytes
	data, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	data[0] = 'X'
	data[1] = 'Y'
	data[2] = 'Z'
	data[3] = '!'
	if err := os.WriteFile(journalPath, data, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	reader, err := OpenJournalReader(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalReader failed: %v", err)
	}
	defer reader.Close()

	_, err = reader.Scan()
	if err == nil {
		t.Fatalf("expected ErrInvalidMagic, got nil")
	}
	if !errors.Is(err, ErrInvalidMagic) {
		t.Fatalf("expected ErrInvalidMagic, got: %v", err)
	}
}

// ============================================================================
// Section 4: Mid-File Bit Flips & Invariant Protection
// ============================================================================

func TestJournal_MidFileBitFlips(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, "midfile_flips.journal")

	writer, err := OpenJournalWriter(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalWriter failed: %v", err)
	}

	var frames []*Frame
	for i := 1; i <= 5; i++ {
		f, err := writer.Append([]byte(fmt.Sprintf("block-content-%d-with-enough-bytes-to-test", i)))
		if err != nil {
			t.Fatalf("Append %d failed: %v", i, err)
		}
		frames = append(frames, f)
	}
	writer.Close()

	targetFrame := frames[2] // 3rd frame (mid-file)

	// Test bit flip in Sequence field (bytes 4..11 of target frame)
	testCorruptionAtOffset := func(byteOffset int64, label string) {
		origBytes, _ := os.ReadFile(journalPath)
		corrupted := make([]byte, len(origBytes))
		copy(corrupted, origBytes)
		corrupted[byteOffset] ^= 0x01

		_ = os.WriteFile(journalPath, corrupted, 0644)

		r, err := OpenJournalReader(journalPath)
		if err != nil {
			t.Fatalf("[%s] OpenJournalReader failed: %v", label, err)
		}
		defer r.Close()

		_, scanErr := r.Scan()
		if scanErr == nil {
			t.Fatalf("[%s] expected corruption error on bit flip at %d, got nil", label, byteOffset)
		}

		// RepairJournal must refuse to truncate mid-file corruptions
		repaired, repErr := RepairJournal(journalPath)
		if repErr == nil && repaired > 0 {
			t.Fatalf("[%s] RepairJournal should not truncate mid-file corruption!", label)
		}

		// Restore file
		_ = os.WriteFile(journalPath, origBytes, 0644)
	}

	testCorruptionAtOffset(targetFrame.Offset+4, "Sequence field bit flip")
	testCorruptionAtOffset(targetFrame.Offset+14, "Timestamp field bit flip")
	testCorruptionAtOffset(targetFrame.Offset+22, "PayloadLen field bit flip")
	testCorruptionAtOffset(targetFrame.Offset+26, "Payload byte bit flip")
	testCorruptionAtOffset(targetFrame.Offset+targetFrame.TotalSize-10, "SHA256 trailer bit flip")
}

// ============================================================================
// Section 5: Partial Frame Truncations & Auto-Repair Tests
// ============================================================================

func TestJournal_PartialFrameTruncation_AutoRepair(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, "partial_trailing.journal")

	writer, err := OpenJournalWriter(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalWriter failed: %v", err)
	}

	for i := 1; i <= 3; i++ {
		if _, err := writer.Append([]byte(fmt.Sprintf("valid-frame-%d", i))); err != nil {
			t.Fatalf("Append %d failed: %v", i, err)
		}
	}
	cleanSize := writer.LastOffset()
	writer.Close()

	// Simulate a crash during append: append partial header (10 bytes of 24)
	f, err := os.OpenFile(journalPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("OpenFile failed: %v", err)
	}
	partialHeader := []byte{'N', 'S', 'J', 'R', 0, 0, 0, 4, 0, 0}
	if _, err := f.Write(partialHeader); err != nil {
		t.Fatalf("Write partial header failed: %v", err)
	}
	f.Close()

	// Re-opening with auto-repair should detect trailing partial frame and truncate it
	reopenedWriter, err := OpenJournalWriter(journalPath, WithAutoRepair(true))
	if err != nil {
		t.Fatalf("OpenJournalWriter with auto-repair failed: %v", err)
	}
	defer reopenedWriter.Close()

	if reopenedWriter.LastOffset() != cleanSize {
		t.Errorf("writer offset after repair = %d, want clean size %d", reopenedWriter.LastOffset(), cleanSize)
	}
	if reopenedWriter.NextSeq() != 4 {
		t.Errorf("writer nextSeq after repair = %d, want 4", reopenedWriter.NextSeq())
	}

	// Verify append resumes seamlessly
	frame4, err := reopenedWriter.Append([]byte("valid-frame-4-after-repair"))
	if err != nil {
		t.Fatalf("Append after repair failed: %v", err)
	}
	if frame4.Seq != 4 {
		t.Errorf("frame4.Seq = %d, want 4", frame4.Seq)
	}
	reopenedWriter.Close()

	// Validate with Reader that all 4 frames are intact
	reader, err := OpenJournalReader(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalReader failed: %v", err)
	}
	defer reader.Close()

	frames, err := reader.Scan()
	if err != nil {
		t.Fatalf("reader.Scan failed: %v", err)
	}
	if len(frames) != 4 {
		t.Fatalf("read %d frames after repair, want 4", len(frames))
	}
	for i, fr := range frames {
		if fr.Seq != uint64(i+1) {
			t.Errorf("frame %d seq = %d, want %d", i, fr.Seq, i+1)
		}
	}
}

func TestJournal_StandaloneRepairJournal(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, "standalone_repair.journal")

	writer, err := OpenJournalWriter(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalWriter failed: %v", err)
	}

	for i := 1; i <= 4; i++ {
		if _, err := writer.Append([]byte(fmt.Sprintf("entry-%d", i))); err != nil {
			t.Fatalf("Append %d failed: %v", i, err)
		}
	}
	cleanSize := writer.LastOffset()
	writer.Close()

	// Append complete header but incomplete payload (cut in half)
	rawBuf, _, _ := EncodeFrame(5, time.Now(), []byte("some-large-payload-that-gets-cut"))
	truncatedBuf := rawBuf[:HeaderSize+5] // cuts off payload and trailer

	f, _ := os.OpenFile(journalPath, os.O_APPEND|os.O_WRONLY, 0644)
	_, _ = f.Write(truncatedBuf)
	f.Close()

	truncatedBytes, err := RepairJournal(journalPath)
	if err != nil {
		t.Fatalf("RepairJournal failed: %v", err)
	}
	if truncatedBytes != int64(len(truncatedBuf)) {
		t.Errorf("truncatedBytes = %d, want %d", truncatedBytes, len(truncatedBuf))
	}

	fi, _ := os.Stat(journalPath)
	if fi.Size() != cleanSize {
		t.Errorf("file size after repair = %d, want clean size %d", fi.Size(), cleanSize)
	}

	// Verify all 4 original records remain readable
	reader, err := OpenJournalReader(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalReader failed: %v", err)
	}
	defer reader.Close()

	frames, err := reader.Scan()
	if err != nil {
		t.Fatalf("reader.Scan after repair failed: %v", err)
	}
	if len(frames) != 4 {
		t.Fatalf("read %d frames, want 4", len(frames))
	}
}

// ============================================================================
// Section 6: Concurrency & Max Payload Protection
// ============================================================================

func TestJournal_ConcurrentAppends(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, "concurrent.journal")

	writer, err := OpenJournalWriter(journalPath, WithSyncOnAppend(false))
	if err != nil {
		t.Fatalf("OpenJournalWriter failed: %v", err)
	}
	defer writer.Close()

	const numWorkers = 8
	const appendsPerWorker = 50
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for w := 0; w < numWorkers; w++ {
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < appendsPerWorker; j++ {
				payload := []byte(fmt.Sprintf("worker-%d-op-%d", workerID, j))
				if _, err := writer.Append(payload); err != nil {
					t.Errorf("worker %d Append failed: %v", workerID, err)
					return
				}
			}
		}(w)
	}

	wg.Wait()
	if err := writer.Sync(); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
	writer.Close()

	reader, err := OpenJournalReader(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalReader failed: %v", err)
	}
	defer reader.Close()

	frames, err := reader.Scan()
	if err != nil {
		t.Fatalf("Scan failed on concurrent journal: %v", err)
	}

	const expectedTotal = numWorkers * appendsPerWorker
	if len(frames) != expectedTotal {
		t.Fatalf("read %d frames, want %d", len(frames), expectedTotal)
	}

	// Verify sequence numbers are strictly 1..expectedTotal
	seenSeqs := make(map[uint64]bool)
	for _, fr := range frames {
		if seenSeqs[fr.Seq] {
			t.Fatalf("duplicate sequence number %d encountered", fr.Seq)
		}
		seenSeqs[fr.Seq] = true
	}
	for s := uint64(1); s <= uint64(expectedTotal); s++ {
		if !seenSeqs[s] {
			t.Fatalf("missing sequence number %d", s)
		}
	}
}

func TestJournal_PayloadSizeLimits(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, "oversize.journal")

	writer, err := OpenJournalWriter(journalPath)
	if err != nil {
		t.Fatalf("OpenJournalWriter failed: %v", err)
	}
	defer writer.Close()

	// Try appending payload larger than MaxPayloadSize
	oversize := make([]byte, MaxPayloadSize+1)
	_, _ = rand.Read(oversize[:1024]) // fill partially

	_, err = writer.Append(oversize)
	if err == nil {
		t.Fatalf("expected ErrPayloadTooLarge, got nil")
	}
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got: %v", err)
	}
}
