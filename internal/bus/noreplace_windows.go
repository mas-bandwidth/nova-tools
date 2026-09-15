//go:build windows

package bus

import (
	"runtime"
	"syscall"
	"unsafe"
)

// noReplaceRenameCall is the call a refusal names when this platform's no-replace rename
// could not publish either.
const noReplaceRenameCall = "MoveFileExW without MOVEFILE_REPLACE_EXISTING"

// moveFileExW is kernel32's rename, reached the way the rest of this package reaches
// Windows: the standard library and no more. golang.org/x/sys is where a typed wrapper
// lives and this repository is standard library only, which is the same trade
// lock_windows.go already makes for LockFileEx.
var moveFileExW = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

// noReplaceRename is Windows's own no-replace rename, and it is the second of the two
// create-exclusive publishes.
//
// MoveFileEx replaces only when it is ASKED to: with no MOVEFILE_REPLACE_EXISTING it fails
// with ERROR_ALREADY_EXISTS or ERROR_FILE_EXISTS when something stands at the destination,
// both of which os.ErrExist matches through syscall.Errno.Is. Refusal is the default here
// and not a flag that has to be remembered, which is the property this whole path is for.
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
	// Both strings must outlive the call: their addresses crossed as integers, which the
	// collector does not follow.
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
