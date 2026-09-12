//go:build windows

package swarm

import (
	"errors"
	"syscall"
)

// transientIO is a Windows collision at a path, and nothing else: the two errors a
// concurrent atomic replace produces. ERROR_SHARING_VIOLATION is a rename over a path a
// reader holds open; ERROR_ACCESS_DENIED is a read of a path whose old file is still
// delete-pending. Neither is a fact about the job whose record it is.
func transientIO(err error) bool {
	if err == nil {
		return false
	}
	const errorSharingViolation = syscall.Errno(32)
	const errorAccessDenied = syscall.Errno(5)
	const errorLockViolation = syscall.Errno(33)
	return errors.Is(err, errorSharingViolation) ||
		errors.Is(err, errorAccessDenied) ||
		errors.Is(err, errorLockViolation)
}
