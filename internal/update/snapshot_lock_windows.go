//go:build windows

package update

import (
	"context"
	"fmt"
	"os"
	"time"
)

// lockSnapshot on Windows takes the lock the unix build took before it moved
// onto internal/filelock: LockFileEx on byte 0 of the sibling file (snapshot_windows.go),
// polled until ctx ends. An old and a new binary on one machine do NOT exclude each other
// because filelock locks one byte at OffsetHigh
// 0x80000000, a range this lock does not overlap.
func lockSnapshot(ctx context.Context, path string) (func(), error) {
	// A stable sibling inode is required because the JSON itself is replaced by
	// rename. The empty lock file survives; only its kernel lock means ownership.
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("cannot open snapshot lock (create the parent directory and check permissions)")
	}
	for {
		ok, err := trySnapshotLock(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("cannot lock snapshot (use a filesystem supporting file locks)")
		}
		if ok {
			return func() { unlockSnapshot(f); f.Close() }, nil
		}
		t := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			f.Close()
			return nil, fmt.Errorf("snapshot is busy (wait for the current report or increase --budget)")
		case <-t.C:
		}
	}
}
