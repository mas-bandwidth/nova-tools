//go:build windows

package main

import (
	"errors"

	"golang.org/x/sys/windows"
)

// osLock is LockFileEx on the first byte of path. The handle is not
// inheritable, so the command never holds the lock, and the kernel lets go of
// it when this process ends, however it ends.
func osLock(path string, exclusive bool) (func(), bool, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, false, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, false, err
	}
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	if err := windows.LockFileEx(h, flags, 0, 1, 0, &windows.Overlapped{}); err != nil {
		// ignored: a handle this process holds no lock on
		_ = windows.CloseHandle(h)
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() {
		// ignored: closing the handle releases the lock even if the unlock failed
		_ = windows.UnlockFileEx(h, 0, 1, 0, &windows.Overlapped{})
		_ = windows.CloseHandle(h)
	}, true, nil
}
