//go:build windows

package merge

import (
	"errors"
	"io/fs"
	"syscall"
)

// THE ATOMIC REPLACE IS THE CONTRACT, and on Windows the reader is what pays for it.
//
// Go opens a file for reading without FILE_SHARE_DELETE, so while MoveFileEx is replacing
// state.json a reader's OPEN can be refused outright -- ERROR_SHARING_VIOLATION, or a
// not-found inside the window where the old name is already gone. That is not a partial
// file and not a lost write: it is a door held shut for the microseconds of a rename. The
// Windows leg of #57 failed there, with every writer's write landing.
//
// The answer is not to give up the rename (nothing else is atomic), not ReplaceFileW, and
// not a promise that holds on unix only: the reader waits the window out. See Load.
const (
	errorSharingViolation = syscall.Errno(32)
	errorAccessDenied     = syscall.Errno(5)
)

// replaceRefusal reports whether this is a refusal a replace in flight explains, and so
// one worth waiting out for a bounded time. Any other error is the caller's answer.
func replaceRefusal(err error) bool {
	return errors.Is(err, errorSharingViolation) ||
		errors.Is(err, errorAccessDenied) ||
		errors.Is(err, fs.ErrNotExist)
}
