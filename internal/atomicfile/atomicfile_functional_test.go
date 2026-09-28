//go:build functional

package atomicfile

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestReaderNeverSeesPartialFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "versioned.txt")

	makeVersionPayload := func(v int) []byte {
		// Large payload (4096 bytes) with recognizable header, body pattern, and trailer
		header := fmt.Sprintf("BEGIN %06d\n", v)
		trailer := fmt.Sprintf("END %06d\n", v)
		pattern := fmt.Sprintf("%08x", v)
		repeatCount := (4096 - len(header) - len(trailer)) / len(pattern)
		body := strings.Repeat(pattern, repeatCount)
		return []byte(header + body + "\n" + trailer)
	}

	// Write initial version 0
	if err := Write(target, makeVersionPayload(0), 0o644); err != nil {
		t.Fatalf("initial write failed: %v", err)
	}

	const totalWrites = 1000
	done := make(chan struct{})
	var closeOnce sync.Once
	stopReader := func() {
		closeOnce.Do(func() {
			close(done)
		})
	}

	var readCount atomic.Int64
	var readErrors []string
	var errMu sync.Mutex

	recordErr := func(s string) {
		errMu.Lock()
		defer errMu.Unlock()
		if len(readErrors) < 10 {
			readErrors = append(readErrors, s)
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)

	// Clean reader teardown guaranteed via defer
	defer func() {
		stopReader()
		wg.Wait()
	}()

	// Reader loop
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				data, err := os.ReadFile(target)
				if err != nil {
					recordErr(fmt.Sprintf("ReadFile error: %v", err))
					continue
				}
				readCount.Add(1)

				// Verify atomic content: must match makeVersionPayload(v) exactly
				var v int
				n, err := fmt.Sscanf(string(data), "BEGIN %d\n", &v)
				if err != nil || n != 1 {
					recordErr(fmt.Sprintf("corrupt header: %q", string(data[:min(len(data), 40)])))
					continue
				}
				expected := makeVersionPayload(v)
				if !bytes.Equal(data, expected) {
					recordErr(fmt.Sprintf("partial read for version %d: len %d, want %d", v, len(data), len(expected)))
				}
			}
		}
	}()

	// Writer writes 1,000 versions sequentially
	for v := 1; v <= totalWrites; v++ {
		if err := Write(target, makeVersionPayload(v), 0o644); err != nil {
			t.Fatalf("Write version %d failed: %v", v, err)
		}
	}

	stopReader()
	wg.Wait()

	if len(readErrors) > 0 {
		t.Fatalf("reader saw %d partial/corrupt files; first error: %s", len(readErrors), readErrors[0])
	}

	t.Logf("Reader completed %d verified reads during %d atomic writes with 0 partial reads", readCount.Load(), totalWrites)

	// Final verification
	finalData, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile final failed: %v", err)
	}
	if !bytes.Equal(finalData, makeVersionPayload(totalWrites)) {
		t.Fatalf("final version not %d", totalWrites)
	}
}

func TestConcurrentWriters(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "concurrent_target.txt")

	const payloadSize = 32768
	payloadA := bytes.Repeat([]byte("A"), payloadSize)
	payloadB := bytes.Repeat([]byte("B"), payloadSize)

	for round := 0; round < 10; round++ {
		var wg sync.WaitGroup
		errs := make(chan error, 2)

		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := Write(target, payloadA, 0o644); err != nil {
				errs <- fmt.Errorf("writer A: %w", err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := Write(target, payloadB, 0o644); err != nil {
				errs <- fmt.Errorf("writer B: %w", err)
			}
		}()

		wg.Wait()
		close(errs)

		for err := range errs {
			t.Fatalf("concurrent writer error: %v", err)
		}

		got, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("ReadFile failed: %v", err)
		}

		isA := bytes.Equal(got, payloadA)
		isB := bytes.Equal(got, payloadB)
		if !isA && !isB {
			t.Fatalf("round %d: target holds corrupted mixed bytes (len=%d)", round, len(got))
		}
	}

	// Verify no temporary files leaked
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "concurrent_target.txt" {
			t.Fatalf("leaked file %q in dir", e.Name())
		}
	}
}

func TestFunctionalPropertyContentAndPermRoundTrip(t *testing.T) {
	t.Parallel()

	const seed = 1
	r := rand.New(rand.NewPCG(seed, 0x6e6f7661))
	dir := t.TempDir()

	for i := 0; i < 200; i++ {
		target := filepath.Join(dir, fmt.Sprintf("func_prop_roundtrip_%d.dat", i))
		data := propertyBytes(r)
		perm := propertyPerms[r.IntN(len(propertyPerms))]

		if err := Write(target, data, perm); err != nil {
			t.Fatalf("seed %d case %d: Write failed: %v", seed, i, err)
		}

		got, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("seed %d case %d: ReadFile failed: %v", seed, i, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("seed %d case %d: read content does not match written (len got=%d, want=%d)", seed, i, len(got), len(data))
		}

		info, err := os.Stat(target)
		if err != nil {
			t.Fatalf("seed %d case %d: Stat failed: %v", seed, i, err)
		}
		if gotPerm := info.Mode().Perm(); gotPerm != perm {
			t.Fatalf("seed %d case %d: perm mismatch: got %04o, want %04o", seed, i, gotPerm, perm)
		}
	}
}

func TestFunctionalPropertyArbitraryOverwrite(t *testing.T) {
	t.Parallel()

	const seed = 2
	r := rand.New(rand.NewPCG(seed, 0x6e6f7661))
	dir := t.TempDir()
	target := filepath.Join(dir, "func_prop_overwrite.dat")

	var current []byte
	for i := 0; i < 200; i++ {
		next := propertyBytes(r)
		perm := propertyPerms[r.IntN(len(propertyPerms))]

		if err := Write(target, next, perm); err != nil {
			t.Fatalf("seed %d case %d: Write failed: %v", seed, i, err)
		}

		got, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("seed %d case %d: ReadFile failed: %v", seed, i, err)
		}
		if !bytes.Equal(got, next) {
			t.Fatalf("seed %d case %d: read mismatch after overwrite (len got=%d, want=%d)", seed, i, len(got), len(next))
		}

		current = next
	}
	_ = current
}

func TestFunctionalPropertyFailureIsolationOverArbitraryData(t *testing.T) {
	t.Parallel()

	const seed = 3
	r := rand.New(rand.NewPCG(seed, 0x6e6f7661))
	dir := t.TempDir()

	failureSteps := []string{"create", "chmod", "write", "sync", "close", "rename"}

	for i := 0; i < 100; i++ {
		target := filepath.Join(dir, fmt.Sprintf("func_prop_fail_%d.dat", i))
		initialData := propertyBytes(r)
		if err := Write(target, initialData, 0o644); err != nil {
			t.Fatalf("seed %d case %d: initial Write failed: %v", seed, i, err)
		}

		badData := propertyBytes(r)
		step := failureSteps[r.IntN(len(failureSteps))]
		injectedErr := errors.New("injected property failure")

		h := defaultHooks()
		switch step {
		case "create":
			h.createTemp = func(dir, base string, perm os.FileMode) (*os.File, error) { return nil, injectedErr }
		case "chmod":
			h.chmod = func(f *os.File, mode os.FileMode) error { return injectedErr }
		case "write":
			h.write = func(f *os.File, data []byte) (int, error) { return 0, injectedErr }
		case "sync":
			h.sync = func(f *os.File) error { return injectedErr }
		case "close":
			h.close = func(f *os.File) error { _ = f.Close(); return injectedErr }
		case "rename":
			h.rename = func(oldpath, newpath string) error { return injectedErr }
		}

		err := writeWithHooks(target, badData, 0o644, h)
		if err == nil {
			t.Fatalf("seed %d case %d: writeWithHooks succeeded at step %s; want error", seed, i, step)
		}

		got, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("seed %d case %d: ReadFile failed after failure: %v", seed, i, err)
		}
		if !bytes.Equal(got, initialData) {
			t.Fatalf("seed %d case %d: target content modified on %s failure!", seed, i, step)
		}
	}
}
