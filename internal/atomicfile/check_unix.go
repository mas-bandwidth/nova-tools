//go:build !windows

package atomicfile

import (
	"io/fs"
	"os"
	"syscall"
)

// The access(2) mode bits, the same on every POSIX system.
const (
	accessX = 0x1
	accessW = 0x2
)

// canWrite reports whether this process can create an entry in dir, asked of
// the kernel (access(2)). access(2) answers for the real user and group, where
// a create uses the effective ones: the two agree for every tool here, which
// runs with no setuid bit, and a plan run under differing credentials is not
// exact.
func canWrite(dir string) error {
	if err := syscall.Access(dir, accessW|accessX); err != nil {
		return &fs.PathError{Op: "access", Path: dir, Err: err}
	}
	return nil
}

// canWriteFile reports whether this process can open path for writing.
func canWriteFile(path string, _ os.FileInfo) error {
	if err := syscall.Access(path, accessW); err != nil {
		return &fs.PathError{Op: "access", Path: path, Err: err}
	}
	return nil
}
