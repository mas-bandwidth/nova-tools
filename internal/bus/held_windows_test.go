package bus

import (
	"os"
	"syscall"
	"testing"
)

// openHeld opens a file for reading in a way that does not stand in the way of the write
// that REPLACES it.
//
// On Windows that has to be asked for. A handle opened without FILE_SHARE_DELETE -- which
// is what os.Open gives, because Go's syscall.Open shares read and write and not delete --
// makes the destination of a rename undeletable, so MoveFileEx fails with "Access is
// denied" and the WRITER fails because a READER is holding the file open. That is a
// property of the handle the reader took, not of the write: with the delete share granted,
// the rename lands and this handle goes on reading the bytes it was opened on, which is
// exactly what it does on unix and exactly what the test is about.
func openHeld(t *testing.T, path string) *os.File {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	return os.NewFile(uintptr(h), path)
}
