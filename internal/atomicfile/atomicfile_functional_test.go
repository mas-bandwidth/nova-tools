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
		header := fmt.Sprintf("BEGIN %06d\n", v)
		trailer := fmt.Sprintf("END %06d\n", v)
		pattern := fmt.Sprintf("%08x", v)
		repeatCount := (4096 - len(header) - len(trailer)) / len(pattern)
		return []byte(header + strings.Repeat(pattern, repeatCount) + "\n" + trailer)
	}

	require.NoError(t, Write(target, makeVersionPayload(0), 0o644), "initial write failed")

	const totalWrites = 1000
	done := make(chan struct{})
	var closeOnce sync.Once
	stopReader := func() { closeOnce.Do(func() { close(done) }) }

	var readCount atomic.Int64
	readErrors := make([]string, 0, 10)
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
	defer func() {
		stopReader()
		wg.Wait()
	}()

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
				var v int
				if n, err := fmt.Sscanf(string(data), "BEGIN %d\n", &v); err != nil || n != 1 {
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

	for v := 1; v <= totalWrites; v++ {
		require.NoError(t, Write(target, makeVersionPayload(v), 0o644), "Write version %d failed", v)
	}

	stopReader()
	wg.Wait()

	firstErr := ""
	if len(readErrors) > 0 {
		firstErr = readErrors[0]
	}
	require.Empty(t, readErrors, "reader saw %d partial/corrupt files; first error: %s", len(readErrors), firstErr)
	t.Logf("Reader completed %d verified reads during %d atomic writes with 0 partial reads", readCount.Load(), totalWrites)

	finalData, err := os.ReadFile(target)
	require.NoError(t, err, "ReadFile final failed")
	require.True(t, bytes.Equal(finalData, makeVersionPayload(totalWrites)), "final version mismatch")
}

func TestConcurrentWriters(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "concurrent_target.txt")
	payloadA := bytes.Repeat([]byte("A"), 32768)
	payloadB := bytes.Repeat([]byte("B"), 32768)

	for round := 0; round < 10; round++ {
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		wg.Add(2)
		go func() { defer wg.Done(); errs <- Write(target, payloadA, 0o644) }()
		go func() { defer wg.Done(); errs <- Write(target, payloadB, 0o644) }()
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err, "concurrent writer error: %v", err)
		}
		got, err := os.ReadFile(target)
		require.NoError(t, err, "ReadFile failed: %v", err)
		require.True(t, bytes.Equal(got, payloadA) || bytes.Equal(got, payloadB),
			"round %d: target holds corrupted mixed bytes (len=%d)", round, len(got))
	}

	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "ReadDir failed: %v", err)
	require.Len(t, entries, 1, "leaked file in dir: %v", entries)
}

func TestFunctionalProperty(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		fn     func(t *testing.T, dir, prefix string, cases, seed int)
		prefix string
		cases  int
		seed   int
	}{
		{"TestFunctionalPropertyContentAndPermRoundTrip", propertyRoundTrip, "func_prop_roundtrip", 200, 1},
		{"TestFunctionalPropertyArbitraryOverwrite", propertyOverwrite, "func_prop_overwrite.dat", 200, 2},
		{"TestFunctionalPropertyFailureIsolationOverArbitraryData", propertyFailureIsolation, "func_prop_fail", 100, 3},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.fn(t, t.TempDir(), tc.prefix, tc.cases, tc.seed)
		})
	}
}
