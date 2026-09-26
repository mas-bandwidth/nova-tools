//go:build darwin

package bus

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Process snapshots are supplied; no foreign process is started. A known
// candidate using this checkout must not disappear solely because of its UID.
func TestStellaKnownForeignGitKeepsItsLock(t *testing.T) {
	t.Parallel()
	for _, platform := range []string{"darwin", "linux-view"} {
		for _, uid := range []uint32{501, 502} {
			t.Run(fmt.Sprintf("%s/uid-%d", platform, uid), func(t *testing.T) {
				t.Parallel()
				hermetic(t)
				dir, lock := oldIndexLock(t)
				args := []string{"git", "-C", dir, "commit"}
				scan := func() ([]gitProc, error) {
					if platform == "darwin" {
						row := fmt.Sprintf("%d 77 %s\n", uid, strings.Join(args, " "))
						return gitProcsFromPS(row, "501", map[string]string{}, nil, func(string) (bool, error) { return true, nil })
					}
					return classifyViews([]procView{{owner: uid, ownerKnown: true, comm: "git", cmdline: []byte(strings.Join(args, "\x00")), cwdErr: os.ErrPermission}}, 501)
				}
				cleared, err := clearStaleIndexLock(dir, time.Now(), scan)
				if cleared {
					t.Fatalf("%s UID %d: removed lock despite a live Git snapshot naming this exact checkout (err=%v)", platform, uid, err)
				}
				if _, err := os.Lstat(lock); err != nil {
					t.Fatalf("lock lost: %v", err)
				}
			})
		}
	}
}
