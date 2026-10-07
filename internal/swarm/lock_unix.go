//go:build unix

package swarm

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/filelock"
)

// takeKernelLock takes internal/filelock's lock on path, waiting until deadline, and
// reports held when another process keeps it. A fresh lock file is created here first,
// exclusively and 0644 as this tool always made it (filelock alone creates 0666 less the
// umask); filelock then opens the existing file and leaves its mode alone. O_EXCL refuses
// whatever already stands at the path, a symlink included; an existing file is filelock's
// to open and check.
func takeKernelLock(path string, deadline time.Time) (func(), bool, error) {
	fresh, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err == nil {
		// ignored: an empty file closed at once; filelock reopens it, and its open reports any fault
		_ = fresh.Close()
	} else if !errors.Is(err, fs.ErrExist) {
		return nil, false, fmt.Errorf("the lock at %s could not be opened: %w", path, err)
	}
	l, err := filelock.Lock(path, "nova-swarm", max(time.Until(deadline), 0))
	if errors.Is(err, filelock.ErrHeld) || errors.Is(err, filelock.ErrBusy) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("the lock at %s could not be taken: %w", path, err)
	}
	// ignored: release has no caller to report to; the kernel lock goes with the descriptor either way
	return func() { _ = l.Unlock() }, false, nil
}
