//go:build windows

package merge

import (
	"os"
	"syscall"
	"unsafe"
)

// The Windows lock, which rule 1 names: "an OS lock the kernel releases on death (`flock`
// on a lock file in the lane directory; the Windows variant is named in the work list),
// NEVER A SENTINEL FILE OR A DIRECTORY", and work list 2 spells it: LockFileEx.
//
// This was an exclusive-create sibling file. It gave the same mutual exclusion and gave
// up the one property rule 2 is built on -- a run killed holding it left the file behind
// and every later run waited its whole budget and refused -- which is the prototype's
// whole lock-file class of bug, kept alive on one platform.
//
// LockFileEx is not in package syscall's Windows symbol table, but the DLL is reachable
// from it, which is how golang.org/x/sys reaches it too. Standard library only, and the
// kernel closes the handle and drops the lock when the process dies however it dies.
var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	lockfileFailImmediately = 0x00000001
	lockfileExclusiveLock   = 0x00000002
	// ERROR_LOCK_VIOLATION: somebody else holds it. That is the answer NO, not a failure.
	errorLockViolation = syscall.Errno(33)
)

// tryLockFile takes an exclusive lock on the whole file without blocking.
func tryLockFile(f *os.File) (bool, error) {
	var ov syscall.Overlapped
	r, _, err := procLockFileEx.Call(f.Fd(), lockfileExclusiveLock|lockfileFailImmediately,
		0, 1, 0, uintptr(unsafe.Pointer(&ov)))
	if r != 0 {
		return true, nil
	}
	if err == errorLockViolation {
		return false, nil
	}
	return false, err
}

func unlockFile(f *os.File) {
	var ov syscall.Overlapped
	_, _, _ = procUnlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&ov)))
}
