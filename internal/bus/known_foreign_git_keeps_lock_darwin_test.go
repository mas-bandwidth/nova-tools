//go:build darwin

package bus

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Process snapshots are supplied; no foreign process is started. A known
// candidate using this checkout must not disappear solely because of its UID.
// The ps half of Stella's probe; the /proc half is TestStellaKnownForeignGitKeepsItsLockLinuxView
// in unreadable_cwd_git_keeps_lock_test.go, built on every OS so it runs on the linux shard.
func TestStellaKnownForeignGitKeepsItsLock(t *testing.T) {
	t.Parallel()
	for _, uid := range []uint32{501, 502} {
		t.Run(fmt.Sprintf("darwin/uid-%d", uid), func(t *testing.T) {
			t.Parallel()
			hermetic(t)
			dir, lock := oldIndexLock(t)
			args := []string{"git", "-C", dir, "commit"}
			scan := func() ([]gitProc, error) {
				row := fmt.Sprintf("%d 77 %s\n", uid, strings.Join(args, " "))
				return gitProcsFromPS(row, "501", map[string]string{}, nil, func(string) (bool, error) { return true, nil })
			}
			cleared, err := clearStaleIndexLock(dir, time.Now(), scan)
			require.False(t, cleared, "darwin UID %d: removed lock despite a live Git snapshot naming this exact checkout (err=%v)", uid, err)
			_, err = os.Lstat(lock)
			require.NoError(t, err, "lock lost: %v", err)
		})
	}
}
