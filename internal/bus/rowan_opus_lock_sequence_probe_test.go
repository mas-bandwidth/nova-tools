package bus

// Cold read of #4420 (rowan-opus): one index.lock walked through the three rules in
// order and back, with the owner seam and the clock moved on the same file.

import (
	"github.com/stretchr/testify/require"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRowanOpusLockSequenceFreshStaleOwnedAndBack(t *testing.T) {
	t.Parallel()
	hermetic(t)
	const self, other = 501, 502
	dir := cloneBus(t, bareBus(t))
	lock, err := indexLockPath(dir)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(lock, nil, 0o644))
	fi, _ := os.Lstat(lock)
	t0 := fi.ModTime()
	ownerUID, ownerOK := uint32(other), true
	seam := func(os.FileInfo) (uint32, bool) { return ownerUID, ownerOK }
	scans := 0
	var scanWith []procView
	scan := func() ([]gitProc, error) { scans++; return classifyViews(scanWith, self) }
	present := func(step string) {
		t.Helper()
		{
			_, e := os.Lstat(lock)
			require.NoError(t, e, "%s: lock gone: %v", step, e)
		}
	}
	denied := &fs.PathError{Op: "readlink", Path: "/proc/9/cwd", Err: syscall.EACCES}

	// 1. fresh, foreign: waiting.
	c, err := clearStaleIndexLockAs(dir, t0.Add(10*time.Second), scan, seam, self)
	require.False(t, c || err != nil || scans != 0, "1 fresh foreign: c=%v err=%v scans=%d", c, err, scans)
	present("1")
	// 2. same lock aged past the threshold, still foreign: refused naming uid, no scan.
	late := t0.Add(staleIndexLockAge + time.Second)
	c, err = clearStaleIndexLockAs(dir, late, scan, seam, self)
	want := "index.lock is owned by uid 502, not this account; ask its owner or the bench admin"
	require.False(t, c || err == nil || err.Error() != want || scans != 0, "2 stale foreign: c=%v err=%v scans=%d", c, err, scans)
	present("2")
	// 2b. owner unreadable: refused, no scan.
	ownerOK = false
	c, err = clearStaleIndexLockAs(dir, late, scan, seam, self)
	require.False(t, c || err != errLockOwnerUnknown || scans != 0, "2b unknown owner: c=%v err=%v", c, err)
	present("2b")
	// 3. handed to the caller, a foreign git names the checkout with -C: kept.
	ownerUID, ownerOK = self, true
	scanWith = []procView{{owner: other, ownerKnown: true, comm: "git\n", cmdline: []byte("git\x00-C\x00" + dir + "\x00commit"), cwdErr: denied}}
	c, err = clearStaleIndexLockAs(dir, late, scan, seam, self)
	require.False(t, c || err != nil || scans != 1, "3 own, foreign -C owner: c=%v err=%v scans=%d", c, err, scans)
	present("3")
	// 4. the caller's own git with unreadable cwd: refused with the old sentence.
	scanWith = []procView{{owner: self, ownerKnown: true, comm: "git\n", cmdline: []byte("git\x00commit"), cwdErr: denied}}
	c, err = clearStaleIndexLockAs(dir, late, scan, seam, self)
	require.False(t, c || err == nil || !strings.HasPrefix(err.Error(), ownershipUnknown), "4 own unreadable: c=%v err=%v", c, err)
	present("4")
	// 5. back: owner flips to foreign again on the same stale lock: refused, no new scan.
	ownerUID = other
	c, err = clearStaleIndexLockAs(dir, late, scan, seam, self)
	require.False(t, c || err == nil || err.Error() != want || scans != 2, "5 back to foreign: c=%v err=%v scans=%d", c, err, scans)
	present("5")
	// 6. back again: lock refreshed (some git touched it): waiting, whoever owns it.
	now := time.Now()
	require.NoError(t, os.Chtimes(lock, now, now))
	c, err = clearStaleIndexLockAs(dir, now.Add(time.Second), scan, seam, self)
	require.False(t, c || err != nil || scans != 2, "6 refreshed foreign: c=%v err=%v", c, err)
	present("6")
	// 7. own, stale, only an unplaced foreign git (foreign by its status uid; round 5 added
	// account/accountKnown to this view, the entry owner alone no longer proves it): cleared.
	ownerUID = self
	scanWith = []procView{{owner: other, ownerKnown: true, account: other, accountKnown: true, comm: "git\n", cmdline: []byte("git\x00commit"), cwdErr: denied}}
	c, err = clearStaleIndexLockAs(dir, now.Add(staleIndexLockAge+time.Second), scan, seam, self)
	require.False(t, !c || err != nil, "7 own stale, unplaced foreign: c=%v err=%v", c, err)
	{
		_, e := os.Lstat(lock)
		require.False(t, !os.IsNotExist(e), "7 lock still here: %v", e)
	}
}
