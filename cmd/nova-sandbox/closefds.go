//go:build linux || darwin

package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"

	"golang.org/x/sys/unix"
)

// fdDir is where the kernel lists this process's open descriptors.
func fdDir() string {
	if runtime.GOOS == "darwin" {
		return "/dev/fd"
	}
	return "/proc/self/fd"
}

// closeInheritedFDs sets close-on-exec on every descriptor above stderr listed in dir,
// so the exec drops them. A descriptor the caller left open is a hole in the wall: it is
// already open, so the kernels do not re-check it, and a socket or a file outside the
// grants would stay usable inside. stdin, stdout and stderr are kept. Marking rather
// than closing is safe because the Go runtime's own descriptors are replaced by the
// exec. Any descriptor it cannot list or mark is an error, never skipped.
func closeInheritedFDs(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	names, err := d.Readdirnames(-1)
	if err != nil {
		return err
	}
	for _, name := range names {
		fd, err := strconv.Atoi(name)
		if err != nil || fd <= 2 || fd == int(d.Fd()) {
			continue
		}
		// EBADF is a descriptor closed since the listing, which is no longer inherited.
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC); err != nil && err != unix.EBADF {
			return fmt.Errorf("descriptor %d: %w", fd, err)
		}
	}
	return nil
}
