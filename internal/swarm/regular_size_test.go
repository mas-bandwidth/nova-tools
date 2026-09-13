package swarm

import (
	"bytes"
	"errors"
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
	if elapsed > 2*time.Second {
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

func TestReadRegularBoundedGrowthAfterStatRefused(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "growing.log")
	if err := os.WriteFile(p, []byte("start"), 0o644); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		chunk := bytes.Repeat([]byte("x"), 128)
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := f.Write(chunk); err != nil {
					return
				}
				time.Sleep(10 * time.Microsecond)
			}
		}
	}()

	// Read with a small bound of 16 bytes while file is growing
	var got []byte
	var err error
	for i := 0; i < 50; i++ {
		got, err = readRegularBounded(p, 16)
		if err != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(stop)
	<-done

	if err == nil {
		t.Fatalf("expected refusal when file grew past 16 bytes, but got %d bytes without error", len(got))
	}
	if got != nil {
		t.Fatalf("expected nil slice on growth refusal, got %d bytes", len(got))
	}
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("expected fs.ErrInvalid, got %v", err)
	}
}
