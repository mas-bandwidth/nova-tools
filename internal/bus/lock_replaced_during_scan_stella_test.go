package bus

import (
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
		if err := os.Rename(lock, lock+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(lock, replacement, 0600); err != nil {
			t.Fatal(err)
		}
		return nil, nil
	})
	got, readErr := os.ReadFile(lock)
	if cleared || readErr != nil || string(got) != string(replacement) {
		t.Fatalf("replacement lock lost: cleared=%v err=%v read=%v contents=%q", cleared, err, readErr, got)
	}
}
