package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A run killed with the lock held leaves the lock file behind -- the filelock
// package never removes it -- and the kernel drops the lock itself when the
// holder dies. The next run therefore takes the lock without waiting for
// anything: the file a dead run left names its holder for a refusal but never
// gates a take.
//
// The bug this closes: while the lock was this package's own, the sentinel
// platforms left a killed run's lock behind and the next run waited out its
// whole budget for a process that was already gone.
func TestLockRecoversWhenTheHolderDied(t *testing.T) {
	t.Parallel()
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	gd, err := GitDir(clone)
	require.NoError(t, err, "the clone's git directory: %v", err)
	lockPath := filepath.Join(gd, LockName)
	// The stamp a dead run left behind: a pid far above the range a kernel
	// hands out, so it names a holder that is not running on any platform.
	dead := fmt.Sprintf("pid=%d\n", 1<<30)
	require.NoError(t, os.WriteFile(lockPath, []byte(dead), 0o644))

	release, err := LockCheckout(clone, 500*time.Millisecond)
	require.NoError(t, err, "a dead run's leftover kept the checkout locked: %v", err)
	release()
}
