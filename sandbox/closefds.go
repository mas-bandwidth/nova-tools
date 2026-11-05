package main

import (
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

// closeInheritedFDs sets close-on-exec on every descriptor above stderr, so the exec
// drops them. A descriptor the caller left open is a hole in the wall: it is already
// open, so the kernels do not re-check it, and a socket or a file outside the grants
// would stay usable inside. stdin, stdout and stderr are kept. Marking rather than
// closing is safe because the Go runtime's own descriptors are replaced by the exec;
// setting the flag now disturbs nothing before it.
func closeInheritedFDs() {
	d, err := os.Open(fdDir())
	if err != nil {
		return
	}
	defer d.Close()
	names, _ := d.Readdirnames(-1)
	for _, name := range names {
		fd, err := strconv.Atoi(name)
		if err != nil || fd <= 2 || fd == int(d.Fd()) {
			continue
		}
		_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC)
	}
}
