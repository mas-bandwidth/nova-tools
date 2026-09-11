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
	// THE LOCKED BYTE IS NOT A BYTE THE FILE HAS. A Windows file lock is MANDATORY, not
	// advisory like flock: while the holder locks bytes 0..0, every other handle's read
	// of that range fails with ERROR_LOCK_VIOLATION -- so the `pid=<n> at=<stamp>` line
	// the holder writes for a waiter to name became unreadable exactly while there was a
	// holder to name, and every Windows refusal said "the holder left no pid".
	//
	// Locking one byte far past any content -- 2^63, which no lock file reaches -- keeps
	// the mutual exclusion (every taker locks the same range) and leaves the line
	// readable. It is the trick SQLite's byte-range locks and Go's own x/sys callers use.
	lockOffsetHigh = 0x80000000
)

// tryLockFile takes the exclusive lock without blocking. See lockOffsetHigh for the range.
func tryLockFile(f *os.File) (bool, error) {
	ov := lockRange()
	r, _, err := procLockFileEx.Call(f.Fd(), lockfileExclusiveLock|lockfileFailImmediately,
		0, 1, 0, uintptr(unsafe.Pointer(ov)))
	if r != 0 {
		return true, nil
	}
	if err == errorLockViolation {
		return false, nil
	}
	return false, err
}

func unlockFile(f *os.File) {
	ov := lockRange()
	_, _, _ = procUnlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(ov)))
}

// lockRange is the one byte both calls name, and they must name the same one: an unlock
// of a range nobody locked leaves the lock held until the process dies.
func lockRange() *syscall.Overlapped {
	return &syscall.Overlapped{OffsetHigh: lockOffsetHigh}
}
