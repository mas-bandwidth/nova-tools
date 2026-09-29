package sprint

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sync"
	"time"
)

// ============================================================================
// Constants and Framing Specification
// ============================================================================

// Magic is the 4-byte header identifying a Nova Sprint Journal frame: "NSJR".
var Magic = [4]byte{'N', 'S', 'J', 'R'}

const (
	// HeaderSize is the fixed size of the frame header:
	// Magic (4 bytes) + Sequence (8 bytes) + Timestamp (8 bytes) + PayloadLen (4 bytes) = 24 bytes.
	HeaderSize = 24

	// TrailerSize is the fixed size of the frame trailer:
	// CRC32-IEEE (4 bytes) + SHA-256 (32 bytes) = 36 bytes.
	TrailerSize = 36

	// FrameOverhead is the total fixed overhead per frame (60 bytes).
	FrameOverhead = HeaderSize + TrailerSize

	// MaxPayloadSize guards against out-of-memory errors on corrupt length headers (64 MiB).
	MaxPayloadSize = 64 * 1024 * 1024
)

var (
	// ErrInvalidMagic indicates that the frame does not begin with Magic bytes "NSJR".
	ErrInvalidMagic = errors.New("journal: invalid magic bytes")

	// ErrCorruptCRC indicates a CRC32-IEEE checksum mismatch.
	ErrCorruptCRC = errors.New("journal: CRC32 checksum mismatch")

	// ErrCorruptSHA256 indicates a SHA-256 integrity hash mismatch.
	ErrCorruptSHA256 = errors.New("journal: SHA-256 integrity hash mismatch")

	// ErrPartialFrame indicates that EOF was reached before completing a frame.
	ErrPartialFrame = errors.New("journal: unexpected EOF reading frame")

	// ErrPayloadTooLarge indicates that payload length exceeds MaxPayloadSize.
	ErrPayloadTooLarge = errors.New("journal: payload length exceeds maximum permitted size")

	// ErrClosed indicates that the reader or writer has been closed.
	ErrClosed = errors.New("journal: file is closed")
)

// ============================================================================
// Frame Data Structure
// ============================================================================

// Frame represents a validated journal record with cryptographic and checksum metadata.
type Frame struct {
	// Seq is the monotonic sequence number of the frame (1-based).
	Seq uint64 `json:"seq"`

	// Timestamp is the recorded time of the entry.
	Timestamp time.Time `json:"timestamp"`

	// PayloadLen is the length of the payload in bytes.
	PayloadLen uint32 `json:"payload_len"`

	// Payload is the raw uncompressed record bytes.
	Payload []byte `json:"payload"`

	// CRC32 is the CRC32-IEEE checksum covering Header + Payload.
	CRC32 uint32 `json:"crc32"`

	// SHA256 is the SHA-256 digest covering Header + Payload + CRC32.
	SHA256 [32]byte `json:"sha256"`

	// Offset is the byte position in the journal file where this frame begins.
	Offset int64 `json:"offset"`

	// TotalSize is the total byte length of this frame (Header + Payload + Trailer).
	TotalSize int64 `json:"total_size"`
}

// EncodeFrame serializes a sequence number, timestamp, and payload into a fully framed,
// checksummed buffer ready for atomic append.
func EncodeFrame(seq uint64, ts time.Time, payload []byte) ([]byte, *Frame, error) {
	if len(payload) > MaxPayloadSize {
		return nil, nil, fmt.Errorf("%w: %d > %d", ErrPayloadTooLarge, len(payload), MaxPayloadSize)
	}

	pLen := len(payload)
	totalSize := HeaderSize + pLen + TrailerSize
	buf := make([]byte, totalSize)

	// 1. Header (24 bytes)
	copy(buf[0:4], Magic[:])
	binary.BigEndian.PutUint64(buf[4:12], seq)
	binary.BigEndian.PutUint64(buf[12:20], uint64(ts.UnixNano()))
	binary.BigEndian.PutUint32(buf[20:24], uint32(pLen))

	// 2. Payload (L bytes)
	copy(buf[24:24+pLen], payload)

	// 3. CRC32-IEEE (4 bytes) computed over Header + Payload
	c := crc32.ChecksumIEEE(buf[:24+pLen])
	binary.BigEndian.PutUint32(buf[24+pLen:28+pLen], c)

	// 4. SHA-256 (32 bytes) computed over Header + Payload + CRC32
	h := sha256.Sum256(buf[:28+pLen])
	copy(buf[28+pLen:totalSize], h[:])

	frame := &Frame{
		Seq:        seq,
		Timestamp:  ts,
		PayloadLen: uint32(pLen),
		Payload:    payload,
		CRC32:      c,
		SHA256:     h,
		TotalSize:  int64(totalSize),
	}

	return buf, frame, nil
}

// DecodeFrame reads and strictly validates a single frame from the provided reader.
// Returns io.EOF if reader is at clean EOF before any byte is read.
func DecodeFrame(r io.Reader, offset int64) (*Frame, error) {
	var header [HeaderSize]byte
	n, err := io.ReadFull(r, header[:])
	if err != nil {
		if errors.Is(err, io.EOF) && n == 0 {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("%w: reading header at offset %d: %v", ErrPartialFrame, offset, err)
	}

	// 1. Validate Magic
	if header[0] != Magic[0] || header[1] != Magic[1] || header[2] != Magic[2] || header[3] != Magic[3] {
		return nil, fmt.Errorf("%w: got %v at offset %d", ErrInvalidMagic, header[:4], offset)
	}

	seq := binary.BigEndian.Uint64(header[4:12])
	tsNano := int64(binary.BigEndian.Uint64(header[12:20]))
	payloadLen := binary.BigEndian.Uint32(header[20:24])

	if payloadLen > MaxPayloadSize {
		return nil, fmt.Errorf("%w: %d at offset %d", ErrPayloadTooLarge, payloadLen, offset)
	}

	// 2. Read Payload + Trailer (TrailerSize = 36 bytes: 4 bytes CRC + 32 bytes SHA256)
	remLen := int(payloadLen) + TrailerSize
	remBuf := make([]byte, remLen)
	_, err = io.ReadFull(r, remBuf)
	if err != nil {
		return nil, fmt.Errorf("%w: reading payload/trailer at offset %d: %v", ErrPartialFrame, offset+HeaderSize, err)
	}

	payload := remBuf[:payloadLen]
	crcBytes := remBuf[payloadLen : payloadLen+4]
	shaBytes := remBuf[payloadLen+4:]

	storedCRC := binary.BigEndian.Uint32(crcBytes)
	var storedSHA [32]byte
	copy(storedSHA[:], shaBytes)

	// 3. Verify CRC32 over Header + Payload
	hCRC := crc32.NewIEEE()
	hCRC.Write(header[:])
	hCRC.Write(payload)
	computedCRC := hCRC.Sum32()
	if computedCRC != storedCRC {
		return nil, fmt.Errorf("%w: computed %08x != stored %08x at offset %d", ErrCorruptCRC, computedCRC, storedCRC, offset)
	}

	// 4. Verify SHA-256 over Header + Payload + CRC32
	hSHA := sha256.New()
	hSHA.Write(header[:])
	hSHA.Write(payload)
	hSHA.Write(crcBytes)
	var computedSHA [32]byte
	hSHA.Sum(computedSHA[:0])

	if computedSHA != storedSHA {
		return nil, fmt.Errorf("%w: at offset %d", ErrCorruptSHA256, offset)
	}

	totalSize := int64(HeaderSize + remLen)
	return &Frame{
		Seq:        seq,
		Timestamp:  time.Unix(0, tsNano),
		PayloadLen: payloadLen,
		Payload:    payload,
		CRC32:      storedCRC,
		SHA256:     storedSHA,
		Offset:     offset,
		TotalSize:  totalSize,
	}, nil
}

// hasSubsequentValidFrame inspects the remaining bytes in a file to determine if
// any subsequent valid frame exists. This distinguishes a mid-file corruption
// from a true trailing partial write.
func hasSubsequentValidFrame(f *os.File, fromOffset, fileSize int64) bool {
	if fileSize-fromOffset < FrameOverhead {
		return false
	}
	buf := make([]byte, 4096)
	searchOffset := fromOffset + 1
	for searchOffset+FrameOverhead <= fileSize {
		if _, err := f.Seek(searchOffset, io.SeekStart); err != nil {
			return false
		}
		n, err := f.Read(buf)
		if n < 4 || (err != nil && !errors.Is(err, io.EOF)) {
			return false
		}
		for i := 0; i <= n-4; i++ {
			if buf[i] == Magic[0] && buf[i+1] == Magic[1] && buf[i+2] == Magic[2] && buf[i+3] == Magic[3] {
				candidateOffset := searchOffset + int64(i)
				if candidateOffset+FrameOverhead <= fileSize {
					if _, seekErr := f.Seek(candidateOffset, io.SeekStart); seekErr == nil {
						if _, decErr := DecodeFrame(f, candidateOffset); decErr == nil {
							return true
						}
					}
				}
			}
		}
		if n <= 3 || err != nil {
			break
		}
		searchOffset += int64(n - 3)
	}
	return false
}

// ============================================================================
// Writer Implementation
// ============================================================================

// WriterOption configures JournalWriter behavior.
type WriterOption func(*JournalWriter)

// WithSyncOnAppend configures whether every append executes an fsync to disk.
func WithSyncOnAppend(sync bool) WriterOption {
	return func(w *JournalWriter) {
		w.syncOnAppend = sync
	}
}

// WithAutoRepair configures whether OpenJournalWriter automatically repairs/truncates
// any partial trailing frame found at the end of the file upon opening.
func WithAutoRepair(autoRepair bool) WriterOption {
	return func(w *JournalWriter) {
		w.autoRepair = autoRepair
	}
}

// JournalWriter manages atomic append-only writing of journal frames with integrity guarantees.
type JournalWriter struct {
	mu           sync.Mutex
	file         *os.File
	path         string
	nextSeq      uint64
	lastOffset   int64
	syncOnAppend bool
	autoRepair   bool
	closed       bool
}

// OpenJournalWriter opens or creates a journal file for append-only streaming.
// It scans existing frames to identify the latest sequence number and file offset.
// If trailing partial frames exist and autoRepair is enabled, it automatically truncates them.
func OpenJournalWriter(path string, opts ...WriterOption) (*JournalWriter, error) {
	jw := &JournalWriter{
		path:         path,
		nextSeq:      1,
		syncOnAppend: true,
		autoRepair:   true,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(jw)
		}
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("journal: open %s: %w", path, err)
	}
	jw.file = f

	stat, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("journal: stat %s: %w", path, err)
	}
	fileSize := stat.Size()

	// Scan existing frames to find last valid frame and check integrity
	var lastOffset int64 = 0
	var lastSeq uint64 = 0

	for {
		frame, err := DecodeFrame(f, lastOffset)
		if err != nil {
			if errors.Is(err, io.EOF) {
				// Clean EOF
				break
			}
			if errors.Is(err, ErrPartialFrame) && jw.autoRepair {
				// Check whether this is a true trailing write or mid-file corruption
				if hasSubsequentValidFrame(f, lastOffset, fileSize) {
					_ = f.Close()
					return nil, fmt.Errorf("journal: mid-file corruption detected at offset %d: %w", lastOffset, err)
				}
				// Partial trailing write detected: truncate to lastOffset
				if truncErr := f.Truncate(lastOffset); truncErr != nil {
					_ = f.Close()
					return nil, fmt.Errorf("journal: repair truncation failed at %d: %w", lastOffset, truncErr)
				}
				if _, seekErr := f.Seek(lastOffset, io.SeekStart); seekErr != nil {
					_ = f.Close()
					return nil, fmt.Errorf("journal: seek after truncation failed: %w", seekErr)
				}
				break
			}
			_ = f.Close()
			return nil, fmt.Errorf("journal: existing journal corrupted at offset %d: %w", lastOffset, err)
		}

		lastOffset += frame.TotalSize
		lastSeq = frame.Seq
	}

	jw.lastOffset = lastOffset
	jw.nextSeq = lastSeq + 1

	// Ensure file write offset is positioned at end
	if _, err := f.Seek(lastOffset, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("journal: seek to end: %w", err)
	}

	return jw, nil
}

// Append writes a payload as a new journal frame with automatic sequence numbering and current timestamp.
func (w *JournalWriter) Append(payload []byte) (*Frame, error) {
	return w.AppendWithTimestamp(payload, time.Now())
}

// AppendWithTimestamp writes a payload with an explicit timestamp.
func (w *JournalWriter) AppendWithTimestamp(payload []byte, ts time.Time) (*Frame, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil, ErrClosed
	}

	rawBytes, frame, err := EncodeFrame(w.nextSeq, ts, payload)
	if err != nil {
		return nil, err
	}

	frame.Offset = w.lastOffset

	// Atomic file append
	n, err := w.file.Write(rawBytes)
	if err != nil {
		return nil, fmt.Errorf("journal: append write failed at offset %d: %w", w.lastOffset, err)
	}
	if n != len(rawBytes) {
		return nil, fmt.Errorf("journal: short write (%d of %d bytes)", n, len(rawBytes))
	}

	if w.syncOnAppend {
		if err := w.file.Sync(); err != nil {
			return nil, fmt.Errorf("journal: sync failed: %w", err)
		}
	}

	w.lastOffset += int64(n)
	w.nextSeq++

	return frame, nil
}

// AppendWithSeq writes a payload with an explicit sequence number and timestamp.
func (w *JournalWriter) AppendWithSeq(seq uint64, ts time.Time, payload []byte) (*Frame, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil, ErrClosed
	}

	rawBytes, frame, err := EncodeFrame(seq, ts, payload)
	if err != nil {
		return nil, err
	}

	frame.Offset = w.lastOffset

	// Atomic file append
	n, err := w.file.Write(rawBytes)
	if err != nil {
		return nil, fmt.Errorf("journal: append write failed at offset %d: %w", w.lastOffset, err)
	}
	if n != len(rawBytes) {
		return nil, fmt.Errorf("journal: short write (%d of %d bytes)", n, len(rawBytes))
	}

	if w.syncOnAppend {
		if err := w.file.Sync(); err != nil {
			return nil, fmt.Errorf("journal: sync failed: %w", err)
		}
	}

	w.lastOffset += int64(n)
	w.nextSeq = seq + 1

	return frame, nil
}


// NextSeq returns the next sequence number that will be assigned.
func (w *JournalWriter) NextSeq() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.nextSeq
}

// LastOffset returns the current byte length of the journal stream.
func (w *JournalWriter) LastOffset() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastOffset
}

// Sync flushes in-memory buffers to stable storage.
func (w *JournalWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	return w.file.Sync()
}

// Close flushes and closes the underlying file descriptor.
func (w *JournalWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if err := w.file.Sync(); err != nil {
		_ = w.file.Close()
		return err
	}
	return w.file.Close()
}

// RepairTrailing scans the journal and truncates any partial trailing write.
// Returns the number of bytes truncated.
func (w *JournalWriter) RepairTrailing() (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, ErrClosed
	}

	stat, err := w.file.Stat()
	if err != nil {
		return 0, err
	}
	fileSize := stat.Size()
	if fileSize == w.lastOffset {
		return 0, nil
	}

	truncated := fileSize - w.lastOffset
	if err := w.file.Truncate(w.lastOffset); err != nil {
		return 0, fmt.Errorf("journal: truncate trailing bytes: %w", err)
	}
	if _, err := w.file.Seek(w.lastOffset, io.SeekStart); err != nil {
		return 0, fmt.Errorf("journal: seek to end after truncate: %w", err)
	}
	return truncated, nil
}

// ============================================================================
// Reader & Replay Iterator Implementation
// ============================================================================

// JournalReader provides read access and replay capabilities over a journal file.
type JournalReader struct {
	path string
	file *os.File
	mu   sync.Mutex
}

// OpenJournalReader opens an existing journal file for reading.
func OpenJournalReader(path string) (*JournalReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("journal: open reader %s: %w", path, err)
	}
	return &JournalReader{
		path: path,
		file: f,
	}, nil
}

// Close closes the underlying reader file.
func (r *JournalReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}

// Scan reads and validates all valid frames from beginning to end.
func (r *JournalReader) Scan() ([]*Frame, error) {
	iter, err := r.NewIterator()
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var frames []*Frame
	for iter.Next() {
		frames = append(frames, iter.Frame())
	}
	if err := iter.Err(); err != nil {
		return frames, err
	}
	return frames, nil
}

// NewIterator creates a replay iterator reading from the start of the journal.
func (r *JournalReader) NewIterator() (*JournalIterator, error) {
	return r.NewIteratorAt(0)
}

// NewIteratorAt creates a replay iterator starting at a specific byte offset.
func (r *JournalReader) NewIteratorAt(offset int64) (*JournalIterator, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil, ErrClosed
	}

	f, err := os.Open(r.path)
	if err != nil {
		return nil, err
	}
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("journal: seek iterator to %d: %w", offset, err)
		}
	}

	return &JournalIterator{
		file:   f,
		offset: offset,
	}, nil
}

// JournalIterator iterates sequentially over journal frames.
type JournalIterator struct {
	file   *os.File
	offset int64
	curr   *Frame
	err    error
	closed bool
}

// Next advances the iterator to the next valid frame. Returns false upon EOF or error.
func (it *JournalIterator) Next() bool {
	if it.closed || it.err != nil {
		return false
	}

	frame, err := DecodeFrame(it.file, it.offset)
	if err != nil {
		if errors.Is(err, io.EOF) {
			it.curr = nil
			return false
		}
		it.err = err
		it.curr = nil
		return false
	}

	it.curr = frame
	it.offset += frame.TotalSize
	return true
}

// Frame returns the current frame from the most recent Next call.
func (it *JournalIterator) Frame() *Frame {
	return it.curr
}

// Err returns the error that stopped iteration, or nil if stopped at clean EOF.
func (it *JournalIterator) Err() error {
	return it.err
}

// Offset returns the current file byte offset of the iterator.
func (it *JournalIterator) Offset() int64 {
	return it.offset
}

// Close closes the iterator's file descriptor.
func (it *JournalIterator) Close() error {
	if it.closed {
		return nil
	}
	it.closed = true
	if it.file != nil {
		return it.file.Close()
	}
	return nil
}

// ============================================================================
// Standalone Journal Recovery Utility
// ============================================================================

// RepairJournal inspects a journal file on disk. If a partial trailing write exists
// at the tail, it truncates the file back to the end of the last complete, valid frame.
// Mid-file corruptions return the corruption error without truncating.
func RepairJournal(path string) (int64, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return 0, err
	}
	fileSize := stat.Size()

	var lastValidOffset int64 = 0
	for {
		frame, err := DecodeFrame(f, lastValidOffset)
		if err != nil {
			if errors.Is(err, io.EOF) {
				// Clean EOF, no repair needed
				return 0, nil
			}
			if errors.Is(err, ErrPartialFrame) {
				// Check whether subsequent valid frames exist (mid-file corruption)
				if hasSubsequentValidFrame(f, lastValidOffset, fileSize) {
					return 0, fmt.Errorf("journal: mid-file corruption detected at offset %d: %w", lastValidOffset, err)
				}
				// True trailing partial write: truncate to lastValidOffset
				truncated := fileSize - lastValidOffset
				if err := f.Truncate(lastValidOffset); err != nil {
					return 0, fmt.Errorf("journal: truncate repair: %w", err)
				}
				return truncated, nil
			}
			// Mid-file corruption or bad magic: report error
			return 0, fmt.Errorf("journal: corruption detected at offset %d: %w", lastValidOffset, err)
		}
		lastValidOffset += frame.TotalSize
	}
}

// ============================================================================
// Journal Mutation Event Protocol
// ============================================================================

// JournalAction names a discrete mutation action recorded in the journal.
type JournalAction string

const (
	ActSetCapacity   JournalAction = "set_capacity"
	ActPush          JournalAction = "push"
	ActRelease       JournalAction = "release"
	ActDealWork      JournalAction = "deal_work"
	ActEndWorkPR     JournalAction = "end_work_pr"
	ActDealRead      JournalAction = "deal_read"
	ActEndReadHigh   JournalAction = "end_read_high"
	ActLand          JournalAction = "land"
	ActEndWorkDone   JournalAction = "end_work_done"
	ActCancelPrimary JournalAction = "cancel_primary"
)

type journalAction = JournalAction

const (
	actSetCapacity   = ActSetCapacity
	actPush          = ActPush
	actRelease       = ActRelease
	actDealWork      = ActDealWork
	actEndWorkPR     = ActEndWorkPR
	actDealRead      = ActDealRead
	actEndReadHigh   = ActEndReadHigh
	actLand          = ActLand
	actEndWorkDone   = ActEndWorkDone
	actCancelPrimary = ActCancelPrimary
)

// JournalMutationEvent models the serializable journal event payload for dual-table operations.
type JournalMutationEvent struct {
	Action    JournalAction `json:"act"`
	Epoch     EpochID       `json:"ep"`
	Actor     string        `json:"actor"`
	CardID    CardID        `json:"card,omitempty"`
	Stream    string        `json:"stream,omitempty"`
	Consumer  ConsumerID    `json:"consumer,omitempty"`
	Slots     int           `json:"slots,omitempty"`
	CopyID    CopyID        `json:"copy,omitempty"`
	Score     float64       `json:"score,omitempty"`
	CommitSHA string        `json:"sha,omitempty"`
	Outcome   CardOutcome   `json:"outcome,omitempty"`
	ToLanded  bool          `json:"to_landed,omitempty"`
	Reason    string        `json:"reason,omitempty"`
}

// ApplyJournalEvent applies a JournalMutationEvent to a MemoryCardMachine.
func ApplyJournalEvent(ctx context.Context, cm *MemoryCardMachine, ev JournalMutationEvent) error {
	opts := WriteOptions{Epoch: ev.Epoch, Actor: ev.Actor}
	switch ev.Action {
	case ActSetCapacity:
		cm.SetConsumerCapacity(ev.Consumer, ev.Slots)
		return nil
	case ActPush:
		_, err := cm.Push(ctx, ev.CardID, ev.Stream, opts)
		return err
	case ActRelease:
		_, err := cm.Release(ctx, ev.CardID, opts)
		return err
	case ActDealWork:
		_, err := cm.DealWork(ctx, DealWorkParams{Card: ev.CardID, Consumer: ev.Consumer, Score: ev.Score}, opts)
		return err
	case ActEndWorkPR:
		_, err := cm.EndWorkPR(ctx, EndWorkPRParams{Copy: ev.CopyID, CommitSHA: ev.CommitSHA}, opts)
		return err
	case ActDealRead:
		_, err := cm.DealRead(ctx, DealReadParams{Card: ev.CardID, Consumer: ev.Consumer, Score: ev.Score}, opts)
		return err
	case ActEndReadHigh:
		_, err := cm.EndReadHigh(ctx, EndReadHighParams{Copy: ev.CopyID, Score: ev.Score}, opts)
		return err
	case ActLand:
		_, err := cm.Land(ctx, ev.CardID, opts)
		return err
	case ActEndWorkDone:
		_, err := cm.EndWorkDone(ctx, EndWorkDoneParams{Copy: ev.CopyID, Outcome: ev.Outcome, ToLanded: ev.ToLanded}, opts)
		return err
	case ActCancelPrimary:
		_, err := cm.CancelPrimary(ctx, CancelPrimaryParams{Card: ev.CardID, Reason: ev.Reason}, opts)
		return err
	default:
		return fmt.Errorf("unknown journal action: %s", ev.Action)
	}
}

func applyJournalEvent(ctx context.Context, cm *MemoryCardMachine, ev JournalMutationEvent) error {
	return ApplyJournalEvent(ctx, cm, ev)
}

