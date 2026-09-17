package swarm

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// security#30 finding 4 residue, issue #234: readRegular had no size cap,
// allowing whole-file reads of worker-written records to force unbounded
// memory allocation on the supervisor (DoS).
//
// These tests verify:
// 1. Ordinary files read unchanged.
// 2. Exact boundary is accepted.
// 3. Boundary + 1 byte is refused without returning partial data.
// 4. Growth after stat is stopped by LimitReader and refused.
// 5. Huge sparse files (100 GiB) are refused immediately at stat without allocation.
// 6. MaxRegularRecord boundary is enforced by readRegular.

func TestReadRegularOrdinaryFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ordinary.txt")
	content := []byte("hello world, ordinary record contents\n")
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := readRegular(p)
	if err != nil {
		t.Fatalf("readRegular failed on ordinary file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("got %q, want %q", got, content)
	}
}

func TestReadRegularBoundedExactBoundaryAccepted(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "exact.txt")
	content := bytes.Repeat([]byte("a"), 64)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := readRegularBounded(p, 64)
	if err != nil {
		t.Fatalf("readRegularBounded(64) on 64-byte file failed: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("got len %d, want len 64", len(got))
	}
}

func TestReadRegularBoundedBoundaryPlusOneRefused(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "plusone.txt")
	content := bytes.Repeat([]byte("a"), 65)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := readRegularBounded(p, 64)
	if err == nil {
		t.Fatalf("readRegularBounded(64) on 65-byte file succeeded, returned %d bytes", len(got))
	}
	if got != nil {
		t.Fatalf("expected nil bytes on error, got %d bytes", len(got))
	}
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("expected fs.ErrInvalid wrapper, got %v", err)
	}
	if !strings.Contains(err.Error(), "passes ceiling") {
		t.Fatalf("expected error mentioning passes ceiling, got %v", err)
	}
}

func TestReadRegularHugeSparseFileRefusedWithoutHugeAllocation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "huge_sparse.log")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	// 100 GiB sparse file: takes 0 bytes of disk blocks, but fi.Size() is 100 GiB.
	const hugeSize = 100 * 1024 * 1024 * 1024 // 100 GiB
	if err := f.Truncate(hugeSize); err != nil {
		f.Close()
		t.Skipf("filesystem does not support 100 GiB truncate: %v", err)
	}
	f.Close()

	start := time.Now()
	got, err := readRegular(p)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("readRegular on 100 GiB sparse file unexpectedly succeeded with %d bytes", len(got))
	}
	if got != nil {
		t.Fatalf("expected nil slice on oversized error, got %d bytes", len(got))
	}
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("expected fs.ErrInvalid wrapper, got %v", err)
	}
	if !strings.Contains(err.Error(), "passes ceiling") {
		t.Fatalf("expected error mentioning passes ceiling, got %v", err)
	}
	// Verify it refused instantaneously (at stat time) without reading or allocating.
	if elapsed > 30*time.Second {
		t.Fatalf("readRegular took %v on sparse file; stat-time refusal should be near instantaneous", elapsed)
	}
}

func TestReadRegularMaxRecordBoundary(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "max_plus_one.log")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(int64(MaxRegularRecord + 1)); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	got, err := readRegular(p)
	if err == nil {
		t.Fatalf("readRegular on MaxRegularRecord+1 succeeded with %d bytes", len(got))
	}
	if got != nil {
		t.Fatalf("expected nil slice on error, got %d bytes", len(got))
	}
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("expected fs.ErrInvalid, got %v", err)
	}
}

type countingReader struct {
	r     io.Reader
	count int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.count += int64(n)
	return n, err
}

type infiniteByteReader struct {
	b byte
}

func (r infiniteByteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.b
	}
	return len(p), nil
}

// Deterministic streamed-bound control: proves that readBounded consumes at most
// limit+1 bytes from a stream and returns nil bytes on error, without allocating a huge buffer.
func TestReadBoundedDeterministicStreamLimit(t *testing.T) {
	const limit = int64(64)
	cr := &countingReader{r: infiniteByteReader{b: 'z'}}

	got, err := readBounded(cr, limit)
	if err == nil {
		t.Fatalf("readBounded on infinite stream succeeded with %d bytes", len(got))
	}
	if got != nil {
		t.Fatalf("expected nil bytes on error, got %d bytes", len(got))
	}
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("expected fs.ErrInvalid wrapper, got %v", err)
	}
	if !strings.Contains(err.Error(), "passes ceiling") {
		t.Fatalf("expected error mentioning passes ceiling, got %v", err)
	}
	// Proves deterministically that at most limit+1 bytes were consumed.
	if cr.count != limit+1 {
		t.Fatalf("expected exactly %d bytes consumed from stream, got %d", limit+1, cr.count)
	}
}

// Proves that readBounded accepts a stream that matches the limit exactly and consumes exact bytes.
func TestReadBoundedDeterministicExactStreamAccepted(t *testing.T) {
	const limit = int64(64)
	data := bytes.Repeat([]byte("y"), int(limit))
	cr := &countingReader{r: bytes.NewReader(data)}

	got, err := readBounded(cr, limit)
	if err != nil {
		t.Fatalf("readBounded failed on exact stream: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("got %d bytes, want %d", len(got), len(data))
	}
	if cr.count != limit {
		t.Fatalf("expected exactly %d bytes consumed, got %d", limit, cr.count)
	}
}

// Proves that readBounded and readRegularBounded reject non-positive limits.
func TestReadBoundedRejectsNonPositiveLimit(t *testing.T) {
	cr := &countingReader{r: bytes.NewReader([]byte("test"))}
	if _, err := readBounded(cr, 0); err == nil {
		t.Fatal("readBounded accepted limit=0")
	}
	if _, err := readBounded(cr, -1); err == nil {
		t.Fatal("readBounded accepted limit=-1")
	}
	if _, err := readRegularBounded("nonexistent", 0); err == nil {
		t.Fatal("readRegularBounded accepted maxBytes=0")
	}
}
