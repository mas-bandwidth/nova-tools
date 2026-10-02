package bus

import (
	"github.com/stretchr/testify/require"
	"os"
	"testing"
	"time"
)

// The scan may overlap another repair or git completing the old lock and a
// new git acquiring a different lock at the same path. Preserve the old inode
// under another name so inode reuse cannot obscure the replacement.
func TestStellaLockReplacedDuringScanIsRetained(t *testing.T) {
	t.Parallel()
	hermetic(t)
	dir, lock := oldIndexLock(t)
	replacement := []byte("new git index lock")
	cleared, err := clearStaleIndexLock(dir, time.Now(), func() ([]gitProc, error) {
		require.NoError(t, os.Rename(lock, lock+".old"))
		require.NoError(t, os.WriteFile(lock, replacement, 0600))
		return nil, nil
	})
	got, readErr := os.ReadFile(lock)
	require.True(t, cleared == false && readErr == nil && string(got) == string(replacement), "replacement lock lost: cleared=%v err=%v read=%v contents=%q", cleared, err, readErr, got)
}
