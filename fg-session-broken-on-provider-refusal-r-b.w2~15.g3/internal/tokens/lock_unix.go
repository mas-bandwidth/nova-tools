//go:build unix

package tokens

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/filelock"
)

// takeFold takes internal/filelock's lock on path, waiting up to wait, and reports held
// when another fold keeps it. A fresh lock file is created here first, exclusively and
// 0644 as this tool always made it (filelock alone creates 0666 less the umask); O_EXCL
// and O_NOFOLLOW refuse whatever already stands at the path, a dangling symlink included,
// so nothing is ever created through a link. filelock then opens the existing file and
// refuses a symlink, a directory, a FIFO or a file replaced between its open and its lock.
func takeFold(path string, wait time.Duration) (func(), bool, error) {
	fresh, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o644)
	if err == nil {
		// ignored: an empty file closed at once; filelock reopens it, and its open reports any fault
		_ = fresh.Close()
	} else if !errors.Is(err, fs.ErrExist) {
		return nil, false, fmt.Errorf("the lock that keeps two folds off one --out could not be opened at %s: %w", path, err)
	}
	l, err := filelock.Lock(path, "nova-tokens fold", wait)
	if errors.Is(err, filelock.ErrHeld) || errors.Is(err, filelock.ErrBusy) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("the lock that keeps two folds off one --out could not be opened at %s: %w", path, err)
	}
	// ignored: release has no caller to report to; the kernel lock goes with the descriptor either way
	return func() { _ = l.Unlock() }, false, nil
}

// HolderPID is the pid in the lock file's holder stamp, or a dash when it holds none. It
// reads filelock's stamp and the bare pid an earlier binary wrote alike. It is read rather
// than scanned for: this tool never looks at another process's command line.
func HolderPID(path string) string {
	st, err := filelock.ReadStamp(path)
	if err != nil || st.PID <= 0 {
		return Dash
	}
	return strconv.Itoa(st.PID)
}
