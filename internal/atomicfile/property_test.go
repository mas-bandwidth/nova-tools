package atomicfile

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The guarantees of atomicfile over arbitrary inputs:
// Fixed seeds so a failure names an input and seed that reproduce deterministically.

func propertyBytes(r *rand.Rand) []byte {
	// Length from 0 up to 32KB
	lengthChoices := []int{0, 1, 2, 7, 64, 255, 1024, 4096, 16384, 32768}
	n := lengthChoices[r.IntN(len(lengthChoices))]
	if r.IntN(2) == 0 {
		n = r.IntN(4096)
	}

	data := make([]byte, n)
	for i := range data {
		data[i] = byte(r.IntN(256))
	}
	return data
}

var propertyPerms = []os.FileMode{
	0o600, 0o644, 0o666, 0o700, 0o755, 0o400, 0o444,
}

// propertyRoundTrip runs cases content-and-perm round trips: each propertyBytes
// payload written at a propertyPerms mode reads back byte for byte at its
// reference permission. Shared by the unit and functional property tests.
func propertyRoundTrip(t *testing.T, dir, prefix string, cases, seed int) {
	t.Helper()
	r := rand.New(rand.NewPCG(uint64(seed), 0x6e6f7661))

	for i := 0; i < cases; i++ {
		name := fmt.Sprintf("%s_%d", prefix, i)
		target := filepath.Join(dir, name+".dat")
		data := propertyBytes(r)
		perm := propertyPerms[r.IntN(len(propertyPerms))]

		err := Write(target, data, perm)
		require.NoError(t, err, "seed %d case %d: Write failed: %v", seed, i, err)

		got, err := os.ReadFile(target)
		require.NoError(t, err, "seed %d case %d: ReadFile failed: %v", seed, i, err)
		require.True(t, bytes.Equal(got, data), "seed %d case %d: read content does not match written (len got=%d, want=%d)", seed, i, len(got), len(data))

		info, err := os.Stat(target)
		require.NoError(t, err, "seed %d case %d: Stat failed: %v", seed, i, err)
		wantPerm := referencePerm(dir, name, perm)
		require.Equal(t, wantPerm, info.Mode().Perm(), "seed %d case %d: perm mismatch: got %04o, want %04o", seed, i, info.Mode().Perm(), wantPerm)
	}
}

// propertyOverwrite overwrites one target with cases successive propertyBytes
// payloads: every read returns the last written payload in full. Shared by the
// unit and functional property tests.
func propertyOverwrite(t *testing.T, dir, name string, cases, seed int) {
	t.Helper()
	r := rand.New(rand.NewPCG(uint64(seed), 0x6e6f7661))
	target := filepath.Join(dir, name)

	for i := 0; i < cases; i++ {
		next := propertyBytes(r)
		perm := propertyPerms[r.IntN(len(propertyPerms))]

		err := Write(target, next, perm)
		require.NoError(t, err, "seed %d case %d: Write failed: %v", seed, i, err)

		got, err := os.ReadFile(target)
		require.NoError(t, err, "seed %d case %d: ReadFile failed: %v", seed, i, err)
		require.True(t, bytes.Equal(got, next), "seed %d case %d: read mismatch after overwrite (len got=%d, want=%d)", seed, i, len(got), len(next))
	}
}

// propertyFailureIsolation plants a failure at one random write step per case:
// the published target keeps the content of the successful initial write, never
// the bad payload. Shared by the unit and functional property tests.
func propertyFailureIsolation(t *testing.T, dir, prefix string, cases, seed int) {
	t.Helper()
	r := rand.New(rand.NewPCG(uint64(seed), 0x6e6f7661))

	failureSteps := []string{"create", "write", "sync", "close", "rename"}

	for i := 0; i < cases; i++ {
		target := filepath.Join(dir, fmt.Sprintf("%s_%d.dat", prefix, i))
		initialData := propertyBytes(r)
		err := Write(target, initialData, 0o644)
		require.NoError(t, err, "seed %d case %d: initial Write failed: %v", seed, i, err)

		badData := propertyBytes(r)
		step := failureSteps[r.IntN(len(failureSteps))]
		injectedErr := errors.New("injected property failure")

		h := defaultHooks()
		switch step {
		case "create":
			h.createTemp = func(dir, base string, perm os.FileMode) (*os.File, error) { return nil, injectedErr }
		case "write":
			h.write = func(f *os.File, data []byte) (int, error) { return 0, injectedErr }
		case "sync":
			h.sync = func(f *os.File) error { return injectedErr }
		case "close":
			h.close = func(f *os.File) error { _ = f.Close(); return injectedErr }
		case "rename":
			h.rename = func(oldpath, newpath string) error { return injectedErr }
		}

		err = writeWithHooks(target, badData, 0o644, h)
		require.Error(t, err, "seed %d case %d: writeWithHooks succeeded at step %s; want error", seed, i, step)

		got, err := os.ReadFile(target)
		require.NoError(t, err, "seed %d case %d: ReadFile failed after failure: %v", seed, i, err)
		require.True(t, bytes.Equal(got, initialData), "seed %d case %d: target content modified on %s failure!", seed, i, step)
	}
}

func TestPropertyContentAndPermRoundTrip(t *testing.T) {
	t.Parallel()

	propertyRoundTrip(t, t.TempDir(), "prop_roundtrip", 20, 1)
}

func TestPropertyArbitraryOverwrite(t *testing.T) {
	t.Parallel()

	propertyOverwrite(t, t.TempDir(), "prop_overwrite.dat", 20, 2)
}

func TestPropertyFailureIsolationOverArbitraryData(t *testing.T) {
	t.Parallel()

	propertyFailureIsolation(t, t.TempDir(), "prop_fail", 20, 3)
}
