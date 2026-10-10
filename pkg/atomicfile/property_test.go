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

// Fixed seeds so a failure names an input and seed that reproduce deterministically.
var propertyPerms = []os.FileMode{
	0o600, 0o644, 0o666, 0o700, 0o755, 0o400, 0o444,
}

func propertyBytes(r *rand.Rand) []byte {
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

// propertyRoundTrip: each propertyBytes payload written at a propertyPerms
// mode reads back byte for byte. Shared by unit and functional property tests.
func propertyRoundTrip(t *testing.T, dir, prefix string, cases, seed int) {
	t.Helper()
	r := rand.New(rand.NewPCG(uint64(seed), 0x6e6f7661))
	for i := 0; i < cases; i++ {
		name := fmt.Sprintf("%s_%d", prefix, i)
		target := filepath.Join(dir, name+".dat")
		data := propertyBytes(r)
		perm := propertyPerms[r.IntN(len(propertyPerms))]
		require.NoError(t, Write(target, data, perm), "seed %d case %d: Write failed", seed, i)
		got, err := os.ReadFile(target)
		require.NoError(t, err, "seed %d case %d: ReadFile failed", seed, i)
		require.True(t, bytes.Equal(got, data), "seed %d case %d: content mismatch (len got=%d, want=%d)", seed, i, len(got), len(data))
		info, err := os.Stat(target)
		require.NoError(t, err, "seed %d case %d: Stat failed", seed, i)
		wantPerm := referencePerm(dir, name, perm)
		require.Equal(t, wantPerm, info.Mode().Perm(), "seed %d case %d: perm mismatch (got %04o, want %04o)", seed, i, info.Mode().Perm(), wantPerm)
	}
}

// propertyOverwrite: every read returns the last written payload in full.
// Shared by unit and functional property tests.
func propertyOverwrite(t *testing.T, dir, name string, cases, seed int) {
	t.Helper()
	r := rand.New(rand.NewPCG(uint64(seed), 0x6e6f7661))
	target := filepath.Join(dir, name)
	for i := 0; i < cases; i++ {
		next := propertyBytes(r)
		perm := propertyPerms[r.IntN(len(propertyPerms))]
		require.NoError(t, Write(target, next, perm), "seed %d case %d: Write failed", seed, i)
		got, err := os.ReadFile(target)
		require.NoError(t, err, "seed %d case %d: ReadFile failed", seed, i)
		require.True(t, bytes.Equal(got, next), "seed %d case %d: read mismatch after overwrite (len got=%d, want=%d)", seed, i, len(got), len(next))
	}
}

// propertyFailureIsolation: the published target keeps the content of the
// successful initial write, never the bad payload. Shared by unit and functional
// property tests.
func propertyFailureIsolation(t *testing.T, dir, prefix string, cases, seed int) {
	t.Helper()
	r := rand.New(rand.NewPCG(uint64(seed), 0x6e6f7661))
	failureSteps := []string{"create", "write", "sync", "close", "rename"}
	for i := 0; i < cases; i++ {
		target := filepath.Join(dir, fmt.Sprintf("%s_%d.dat", prefix, i))
		initialData := propertyBytes(r)
		require.NoError(t, Write(target, initialData, 0o644), "seed %d case %d: initial Write failed", seed, i)

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
		require.Error(t, writeWithHooks(target, badData, 0o644, h), "seed %d case %d: writeWithHooks succeeded at step %s", seed, i, step)
		got, err := os.ReadFile(target)
		require.NoError(t, err, "seed %d case %d: ReadFile failed after failure", seed, i)
		require.True(t, bytes.Equal(got, initialData), "seed %d case %d: target content modified on %s failure", seed, i, step)
	}
}

func TestProperty(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		fn     func(t *testing.T, dir, prefix string, cases, seed int)
		prefix string
		cases  int
		seed   int
	}{
		{"TestPropertyContentAndPermRoundTrip", propertyRoundTrip, "prop_roundtrip", 20, 1},
		{"TestPropertyArbitraryOverwrite", propertyOverwrite, "prop_overwrite.dat", 20, 2},
		{"TestPropertyFailureIsolationOverArbitraryData", propertyFailureIsolation, "prop_fail", 20, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.fn(t, t.TempDir(), tc.prefix, tc.cases, tc.seed)
		})
	}
}
