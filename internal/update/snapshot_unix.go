//go:build unix

package update

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/filelock"
)

// lockSnapshot takes the snapshot's lock, waiting until ctx ends.
//
// A stable sibling inode is required because the JSON itself is replaced by rename. The
// lock is pkg/filelock's (tla/FileLock.tla): the same flock on the same sibling file
// the earlier binaries took, so an old and a new nova-update still exclude each other
// during an upgrade (snapshot_compat_unix_test.go). Only the kernel lock means
// ownership; the file survives, and while held it carries the holder's stamp, which
// nothing reads. A fresh file is created here first, exclusively and 0600 as this tool
// always made it (filelock alone creates 0666 less the umask). The wait is this caller's,
// bounded by ctx: take again until the budget ends, which the model calls waiting and
// gives no state.
func lockSnapshot(ctx context.Context, path string) (func(), error) {
	lock := path + ".lock"
	fresh, err := os.OpenFile(lock, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		// ignored: an empty file closed at once; filelock reopens it, and its open reports any fault
		_ = fresh.Close()
	} else if !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("cannot open snapshot lock (create the parent directory and check permissions)")
	}
	for {
		l, err := filelock.TryLock(lock, "nova-update report --snapshot")
		if err == nil {
			// ignored: release has no caller to report to; the kernel lock goes with the descriptor either way
			return func() { _ = l.Unlock() }, nil
		}
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
			return nil, fmt.Errorf("cannot open snapshot lock (create the parent directory and check permissions)")
		}
		if !errors.Is(err, filelock.ErrHeld) && !errors.Is(err, filelock.ErrBusy) {
			return nil, fmt.Errorf("cannot lock snapshot (use a regular file on a filesystem supporting file locks)")
		}
		t := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, fmt.Errorf("snapshot is busy (wait for the current report or increase --budget)")
		case <-t.C:
		}
	}
}
