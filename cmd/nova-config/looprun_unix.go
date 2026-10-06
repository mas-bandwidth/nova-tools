//go:build unix

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// platformLoopLock takes a non-blocking exclusive lock and writes this
// process's pid. A lock already held is not an error: held is false.
func platformLoopLock(path string) (loopHold, bool, error) {
	if cur, err := os.Lstat(path); err == nil && cur.Mode()&os.ModeSymlink != 0 {
		return loopHold{}, false, fmt.Errorf("%s is a symlink", oneline.Field(path))
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return loopHold{}, false, fmt.Errorf("the lock %s could not be opened", oneline.Field(path))
	}
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		// ignored: the lock was not taken, so closing it drops nothing another process holds
		_ = f.Close()
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return loopHold{}, false, nil
		}
		return loopHold{}, false, fmt.Errorf("the lock %s could not be taken", oneline.Field(path))
	}
	if err := f.Truncate(0); err != nil {
		// ignored: the lock is dropped because the pid was not written
		_ = f.Close()
		return loopHold{}, false, err
	}
	if _, err := f.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err != nil {
		// ignored: the lock is dropped because the pid was not written
		_ = f.Close()
		return loopHold{}, false, err
	}
	hold := loopHold{
		release: func() {
			// ignored: the process is leaving the lock; close drops it
			_ = f.Close()
		},
		clear: func() error {
			_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), uintptr(syscall.F_SETFD), 0)
			if errno != 0 {
				return errno
			}
			return nil
		},
	}
	return hold, true, nil
}

// platformExec replaces this process with argv. A program with no slash is
// looked up on PATH. syscall.Exec does not return on success.
func platformExec(argv []string) error {
	if len(argv) == 0 || argv[0] == "" {
		return fmt.Errorf("no program")
	}
	prog := argv[0]
	args := append([]string{}, argv...)
	if !strings.Contains(prog, "/") {
		found, err := exec.LookPath(prog)
		if err != nil {
			return err
		}
		prog = found
		args[0] = found
	}
	return syscall.Exec(prog, args, os.Environ())
}
