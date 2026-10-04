//go:build functional

package atomicfile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
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
	err := Write(target, makeVersionPayload(0), 0o644)
	require.NoError(t, err, "initial write failed: %v", err)

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
		err := Write(target, makeVersionPayload(v), 0o644)
		require.NoError(t, err, "Write version %d failed: %v", v, err)
	}

	stopReader()
	wg.Wait()

	firstErr := ""
	if len(readErrors) > 0 {
		firstErr = readErrors[0]
	}
	require.Empty(t, readErrors, "reader saw %d partial/corrupt files; first error: %s", len(readErrors), firstErr)

	t.Logf("Reader completed %d verified reads during %d atomic writes with 0 partial reads", readCount.Load(), totalWrites)

	// Final verification
	finalData, err := os.ReadFile(target)
	require.NoError(t, err, "ReadFile final failed: %v", err)
	require.True(t, bytes.Equal(finalData, makeVersionPayload(totalWrites)), "final version not %d", totalWrites)
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
			require.NoError(t, err, "concurrent writer error: %v", err)
		}

		got, err := os.ReadFile(target)
		require.NoError(t, err, "ReadFile failed: %v", err)

		isA := bytes.Equal(got, payloadA)
		isB := bytes.Equal(got, payloadB)
		require.True(t, isA || isB, "round %d: target holds corrupted mixed bytes (len=%d)", round, len(got))
	}

	// Verify no temporary files leaked
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "ReadDir failed: %v", err)
	for _, e := range entries {
		require.Equal(t, "concurrent_target.txt", e.Name(), "leaked file %q in dir", e.Name())
	}
}

func TestFunctionalPropertyContentAndPermRoundTrip(t *testing.T) {
	t.Parallel()

	propertyRoundTrip(t, t.TempDir(), "func_prop_roundtrip", 200, 1)
}

func TestFunctionalPropertyArbitraryOverwrite(t *testing.T) {
	t.Parallel()

	propertyOverwrite(t, t.TempDir(), "func_prop_overwrite.dat", 200, 2)
}

func TestFunctionalPropertyFailureIsolationOverArbitraryData(t *testing.T) {
	t.Parallel()

	propertyFailureIsolation(t, t.TempDir(), "func_prop_fail", 100, 3)
}
