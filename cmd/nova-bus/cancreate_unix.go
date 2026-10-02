//go:build !windows

package main

import (
	"io/fs"
	"syscall"
)

// canCreateIn reports whether this process can create an entry in dir, asked of the kernel
// (access(2), write and search), which answers for the process's own credentials and reads
// nothing but the directory's mode. It is internal/atomicfile's canWrite, which that package
// does not export: its exported Check also refuses a symlinked parent and a symlink target,
// which draft --out follows and replaces, so Check would refuse paths the write accepts.
func canCreateIn(dir string) error {
	const accessW, accessX = 0x2, 0x1
	if err := syscall.Access(dir, accessW|accessX); err != nil {
		return &fs.PathError{Op: "access", Path: dir, Err: err}
	}
	return nil
}
