//go:build windows

package update

import (
	"os"
	"syscall"
	"unsafe"
)

var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var lockFileEx = kernel32.NewProc("LockFileEx")
var unlockFileEx = kernel32.NewProc("UnlockFileEx")

func trySnapshotLock(f *os.File) (bool, error) {
	var o syscall.Overlapped
	r, _, e := lockFileEx.Call(f.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(&o)))
	if r != 0 {
		return true, nil
	}
	if e == syscall.Errno(33) {
		return false, nil
	}
	return false, e
}
func unlockSnapshot(f *os.File) {
	var o syscall.Overlapped
	unlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&o)))
}
