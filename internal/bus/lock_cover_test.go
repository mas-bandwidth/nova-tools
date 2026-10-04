package bus

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A bus that is not a git checkout has nothing to lock: the take answers a
// release that is safe to defer and no error, and writes no lock file anywhere.
func TestLockCoverLockCheckoutWithoutAGitCheckout(t *testing.T) {
	t.Parallel()

	release, err := LockCheckout(t.TempDir(), 0)
	require.NoError(t, err, "a bus that is not a checkout was refused: %v", err)
	require.NotNil(t, release, "the nothing-to-lock answer has a release to defer")
	release()
}

// A lock file that cannot be opened is a refusal that names the lock, not a
// wait for a holder: the checkout's git directory holds a nova-bus.lock that
// is a directory, and the take refuses over it rather than working beside
// whatever left it there.
func TestLockCoverLockCheckoutRefusesAnUnopenableLock(t *testing.T) {
	t.Parallel()
	hermetic(t)

	clone := cloneBus(t, bareBus(t))
	gd, err := GitDir(clone)
	require.NoError(t, err, "the clone's git directory: %v", err)
	require.NoError(t, os.Mkdir(filepath.Join(gd, LockName), 0o755), "staging the unopenable lock")

	_, err = LockCheckout(clone, 0)
	require.Error(t, err, "a lock that cannot be opened was taken")
	assert.Contains(t, err.Error(), "could not be opened", "the refusal names the unopenable lock: %v", err)
	assert.NotErrorIs(t, err, ErrLockHeld, "an unopenable lock is not a held one")
}
