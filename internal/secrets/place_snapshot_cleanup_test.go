package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errRemovalRefused is the failure the swapped remover reports.
var errRemovalRefused = errors.New("test: the snapshot's removal failed")

// failSnapshotRemoval swaps removeUnder for one that removes the directory (so the test
// leaves nothing behind) and then reports errRemovalRefused, so decryptSnapshot sees a
// failed cleanup every time. It returns where the snapshot directory was. The swap is a
// package var, so callers never run in parallel.
func failSnapshotRemoval(t *testing.T) *string {
	t.Helper()
	real := removeUnder
	t.Cleanup(func() { removeUnder = real })
	removed := new(string)
	removeUnder = func(root, path string) error {
		*removed = path
		require.NoError(t, real(root, path))
		return errRemovalRefused
	}
	return removed
}

// sealedRowan is the fixture's sealed seat file and its bytes as place reads them.
func sealedRowan(t *testing.T, f *seatFixture) (string, []byte) {
	t.Helper()
	file := filepath.Join(f.storeDir, "rowan.yaml")
	sealed, err := os.ReadFile(file)
	require.NoError(t, err)
	return file, sealed
}

// TestDecryptSnapshotJoinsAFailedRemovalWithTheDecryptError: the decrypt fails (air's key
// is not a recipient of rowan.yaml) AND the snapshot's removal fails. Both errors come
// back, the removal's no longer hidden behind the decrypt's, and no plaintext does.
func TestDecryptSnapshotJoinsAFailedRemovalWithTheDecryptError(t *testing.T) {
	f := newSeatFixture(t)
	file, sealed := sealedRowan(t, f)
	removed := failSnapshotRemoval(t)

	out, err := decryptSnapshot(f.sopsPath, f.airKey, file, sealed)
	require.Error(t, err)
	assert.Empty(t, out, "no plaintext comes back from a place whose snapshot was not removed")
	assert.ErrorIs(t, err, errRemovalRefused, "the removal failure is kept beside the decrypt's")

	joined, ok := err.(interface{ Unwrap() []error })
	require.True(t, ok, "both errors are kept by errors.Join; got %T", err)
	errs := joined.Unwrap()
	require.Len(t, errs, 2)
	// The decrypt's error is sanitized by DecryptFile and carries no chain, so it is
	// found by what it says: the sops failure, naming the store's file, not the snapshot.
	assert.ErrorContains(t, errs[0], "sops failed")
	assert.ErrorContains(t, errs[0], file)
	assert.NotContains(t, errs[0].Error(), *removed)
	assert.ErrorIs(t, errs[1], errRemovalRefused)
	assert.ErrorContains(t, errs[1], "cannot remove the private snapshot "+*removed)
}

// TestDecryptSnapshotFailsAndClearsWhenOnlyTheRemovalFails: the decrypt succeeds, the
// removal fails, and the call returns the removal's error and no plaintext.
func TestDecryptSnapshotFailsAndClearsWhenOnlyTheRemovalFails(t *testing.T) {
	f := newSeatFixture(t)
	file, sealed := sealedRowan(t, f)

	// Guard: with the real remover this decrypt succeeds, so the failure below is the
	// removal's alone.
	out, err := decryptSnapshot(f.sopsPath, f.rowanKey, file, sealed)
	require.NoError(t, err)
	require.Contains(t, string(out), "GH_TOKEN")

	removed := failSnapshotRemoval(t)
	out, err = decryptSnapshot(f.sopsPath, f.rowanKey, file, sealed)
	require.Error(t, err)
	assert.Empty(t, out, "no plaintext comes back from a place whose snapshot was not removed")
	assert.ErrorIs(t, err, errRemovalRefused)
	assert.ErrorContains(t, err, "cannot remove the private snapshot "+*removed)
	assert.NotContains(t, err.Error(), "ghp_carried", "the error never carries the value")
}
