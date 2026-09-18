//go:build windows

package swarm

import (
	"runtime"
	"syscall"
	"unsafe"
)

// moveFileExW is kernel32's rename. MoveFileEx replaces only when it is asked to:
// with no MOVEFILE_REPLACE_EXISTING (flag 0), it fails with ERROR_ALREADY_EXISTS
// or ERROR_FILE_EXISTS when something stands at the destination.
var moveFileExW = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func noReplaceRename(from, to string) error {
	fromp, err := syscall.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	top, err := syscall.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	r1, _, e1 := moveFileExW.Call(uintptr(unsafe.Pointer(fromp)), uintptr(unsafe.Pointer(top)), 0)
	runtime.KeepAlive(fromp)
	runtime.KeepAlive(top)
	if r1 == 0 {
		if e1 != nil {
			return e1
		}
		return syscall.EINVAL
	}
	return nil
}
