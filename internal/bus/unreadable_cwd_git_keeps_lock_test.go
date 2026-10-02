package bus

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The /proc half of Stella's probe (known_foreign_git_keeps_its_lock_darwin_test.go holds the ps
// half): a supplied /proc view of a git, of this account or another, naming this checkout
// with -C and whose cwd cannot be read, keeps the lock. No build tag, so the case runs on
// the linux shard as well as on darwin; the view is injected, no foreign process is started.
func TestStellaKnownForeignGitKeepsItsLockLinuxView(t *testing.T) {
	t.Parallel()
	for _, uid := range []uint32{501, 502} {
		t.Run(fmt.Sprintf("linux-view/uid-%d", uid), func(t *testing.T) {
			t.Parallel()
			hermetic(t)
			dir, lock := oldIndexLock(t)
			args := []string{"git", "-C", dir, "commit"}
			scan := func() ([]gitProc, error) {
				return classifyViews([]procView{{owner: uid, ownerKnown: true, account: uid, accountKnown: true, comm: "git", cmdline: []byte(strings.Join(args, "\x00")), cwdErr: os.ErrPermission}}, 501)
			}
			cleared, err := clearStaleIndexLock(dir, time.Now(), scan)
			require.False(t, cleared, "linux-view UID %d: removed lock despite a live Git snapshot naming this exact checkout (err=%v)", uid, err)
			_, err = os.Lstat(lock)
			require.NoError(t, err, "lock lost: %v", err)
		})
	}
}
