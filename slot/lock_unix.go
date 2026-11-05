//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package main

import (
	"errors"
	"os"
	"syscall"
)

// osLock is flock(2) on path. The file is opened read-only (flock needs no
// write access, so every user can lock a file another user made), and opened
// before it is created, which a sticky /tmp refuses for another user's file.
// Go opens it close-on-exec, so the command never inherits the lock.
func osLock(path string, exclusive bool) (func(), bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		f, err = os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o666)
	}
	if err != nil {
		return nil, false, err
	}
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB); err != nil {
		// ignored: a file this process holds no lock on
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, err
	}
	// ignored: closing the descriptor is the release, and cannot fail to release
	return func() { _ = f.Close() }, true, nil
}
