package atomicfile

import (
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

func TestPropertyContentAndPermRoundTrip(t *testing.T) {
	t.Parallel()

	const seed = 1
	r := rand.New(rand.NewPCG(seed, 0x6e6f7661))
	dir := t.TempDir()

	for i := 0; i < 20; i++ {
		target := filepath.Join(dir, fmt.Sprintf("prop_roundtrip_%d.dat", i))
		data := propertyBytes(r)
		perm := propertyPerms[r.IntN(len(propertyPerms))]

		err := Write(target, data, perm)
		require.NoError(t, err, "seed %d case %d: Write failed: %v", seed, i, err)

		got, err := os.ReadFile(target)
		require.NoError(t, err, "seed %d case %d: ReadFile failed: %v", seed, i, err)
		require.Equal(t, data, got, "seed %d case %d: read content does not match written (len got=%d, want=%d)", seed, i, len(got), len(data))

		info, err := os.Stat(target)
		require.NoError(t, err, "seed %d case %d: Stat failed: %v", seed, i, err)
		wantPerm := referencePerm(dir, fmt.Sprintf("prop_roundtrip_%d", i), perm)
		require.Equal(t, wantPerm, info.Mode().Perm(), "seed %d case %d: perm mismatch: got %04o, want %04o", seed, i, info.Mode().Perm(), wantPerm)
	}
}

func TestPropertyArbitraryOverwrite(t *testing.T) {
	t.Parallel()

	const seed = 2
	r := rand.New(rand.NewPCG(seed, 0x6e6f7661))
	dir := t.TempDir()
	target := filepath.Join(dir, "prop_overwrite.dat")

	var current []byte
	for i := 0; i < 20; i++ {
		next := propertyBytes(r)
		perm := propertyPerms[r.IntN(len(propertyPerms))]

		err := Write(target, next, perm)
		require.NoError(t, err, "seed %d case %d: Write failed: %v", seed, i, err)

		got, err := os.ReadFile(target)
		require.NoError(t, err, "seed %d case %d: ReadFile failed: %v", seed, i, err)
		require.Equal(t, next, got, "seed %d case %d: read mismatch after overwrite (len got=%d, want=%d)", seed, i, len(got), len(next))

		current = next
	}
	_ = current
}

func TestPropertyFailureIsolationOverArbitraryData(t *testing.T) {
	t.Parallel()

	const seed = 3
	r := rand.New(rand.NewPCG(seed, 0x6e6f7661))
	dir := t.TempDir()

	failureSteps := []string{"create", "write", "sync", "close", "rename"}

	for i := 0; i < 20; i++ {
		target := filepath.Join(dir, fmt.Sprintf("prop_fail_%d.dat", i))
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
		require.Equal(t, initialData, got, "seed %d case %d: target content modified on %s failure!", seed, i, step)
	}
}
