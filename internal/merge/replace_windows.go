//go:build windows

package merge

import (
	"errors"
	"io"
	"io/fs"
	"os"
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

// readShared reads path with FILE_SHARE_DELETE in the share mode, which is the half of
// the window this tool can close outright rather than wait out.
//
// os.ReadFile opens with FILE_SHARE_READ|FILE_SHARE_WRITE and no DELETE, and while such a
// handle is open MoveFileEx cannot replace the file: the READER is what refuses the
// writer's rename with "Access is denied". Under thirty writers and two readers polling
// the lane, one write was lost to exactly that. A reader that shares delete is no longer
// the door held shut -- the rename goes through underneath it, and the bytes this handle
// was already reading are the old file's, which is what an atomic replace has always
// promised a reader that got in first.
//
// The wait in readState stays: another process (a person's editor, another tool) may hold
// the file without sharing delete, and an open refused inside a replace window is still
// worth looking again for.
func readShared(path string) ([]byte, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := syscall.CreateFile(
		name,
		syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(h), path)
	defer f.Close()
	return io.ReadAll(f)
}
