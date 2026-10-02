package bus

// Cold read of #4420 at 7deb7afb2 (rowan-opus), probes (a) and (c) as one sequence on one
// path: an own git holds the lock; a foreign git's lock takes the path during the scan;
// back to foreign-and-stale; then an own lock replaced during the scan by a file that
// carries the SAME owner and the SAME old mtime, so only os.SameFile can tell them apart;
// then the unchanged lock is cleared. Owners are read through the seam, keyed by inode.

import (
	"errors"
	"github.com/stretchr/testify/require"
	"os"
	"sync"
	"testing"
	"time"
)

func TestRowanOpusLockReplacedDuringScanSequence(t *testing.T) {
	t.Parallel()
	hermetic(t)
	self := effectiveUID()
	other := self + 1
	dir, lock := oldIndexLock(t)
	fi0, err := os.Lstat(lock)
	require.NoError(t, err)
	old := fi0.ModTime()

	var mu sync.Mutex
	type owned struct {
		fi  os.FileInfo
		uid uint32
	}
	var owners []owned
	own := func(uid uint32) { // the file now at the path is owned by uid
		fi, err := os.Lstat(lock)
		require.NoError(t, err)
		mu.Lock()
		owners = append(owners, owned{fi, uid})
		mu.Unlock()
	}
	seam := func(fi os.FileInfo) (uint32, bool) {
		mu.Lock()
		defer mu.Unlock()
		for i := len(owners) - 1; i >= 0; i-- {
			if os.SameFile(owners[i].fi, fi) {
				return owners[i].uid, true
			}
		}
		return 0, false
	}
	// replace puts a new inode at the path with the inspected lock's mtime, so neither the
	// age rule nor the mtime clause can see the swap; the old inode is kept under another
	// name so it cannot be reused.
	n := 0
	replace := func(body string, uid uint32) {
		n++
		require.NoError(t, os.Rename(lock, lock+".old"+string(rune('0'+n))))
		require.NoError(t, os.WriteFile(lock, []byte(body), 0o600))
		require.NoError(t, os.Chtimes(lock, old, old))
		own(uid)
	}
	body := func(step, want string) {
		t.Helper()
		got, err := os.ReadFile(lock)
		require.NoError(t, err, "%s: lock at the path = %q, %v; want %q", step, got, err, want)
		require.Equal(t, want, string(got), "%s: lock at the path = %q, %v; want %q", step, got, err, want)
	}
	own(self)
	now := time.Now()
	ownGit := []gitProc{{command: "git -C " + dir + " commit", args: []string{"git", "-C", dir, "commit"}}}

	// 1. own stale lock, own git holds the checkout: kept, no error.
	c, err := clearStaleIndexLockAs(dir, now, func() ([]gitProc, error) { return ownGit, nil }, seam, self)
	require.False(t, c, "1 own git holds: c=%v err=%v", c, err)
	require.NoError(t, err, "1 own git holds: c=%v err=%v", c, err)
	body("1", "")
	// 2. (a) during the scan the own git finishes and a foreign git's lock takes the path.
	c, err = clearStaleIndexLockAs(dir, now, func() ([]gitProc, error) { replace("foreign", other); return nil, nil }, seam, self)
	require.False(t, c || !errors.Is(err, ErrIndexLockChanged), "2 foreign replaced during scan: c=%v err=%v", c, err)
	body("2", "foreign")
	// 3. back: the foreign lock, stale, is refused by owner before any scan.
	scanned := false
	c, err = clearStaleIndexLockAs(dir, now, func() ([]gitProc, error) { scanned = true; return nil, nil }, seam, self)
	require.False(t, c, "3 foreign stale: c=%v err=%v scanned=%v", c, err, scanned)
	require.Error(t, err, "3 foreign stale: c=%v err=%v scanned=%v", c, err, scanned)
	require.Equal(t, lockOwnerForeignErr(other).Error(), err.Error(), "3 foreign stale: c=%v err=%v scanned=%v", c, err, scanned)
	require.False(t, scanned, "3 foreign stale: c=%v err=%v scanned=%v", c, err, scanned)
	body("3", "foreign")
	// 4. (c) the path is own again; during the scan it is replaced by another own lock
	// with the same owner and the same mtime: only the inode differs. Not removed.
	replace("own-a", self)
	c, err = clearStaleIndexLockAs(dir, now, func() ([]gitProc, error) { replace("own-b", self); return nil, nil }, seam, self)
	require.False(t, c || !errors.Is(err, ErrIndexLockChanged), "4 same owner, same mtime, new inode: c=%v err=%v", c, err)
	body("4", "own-b")
	// 5. the lock that stayed, unchanged through a scan: cleared.
	c, err = clearStaleIndexLockAs(dir, now, func() ([]gitProc, error) { return nil, nil }, seam, self)
	require.True(t, c, "5 unchanged: c=%v err=%v", c, err)
	require.NoError(t, err, "5 unchanged: c=%v err=%v", c, err)
	{
		_, err := os.Lstat(lock)
		require.True(t, os.IsNotExist(err), "5 lock still here: %v", err)
	}
}
