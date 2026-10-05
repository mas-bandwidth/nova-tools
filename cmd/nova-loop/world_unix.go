//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// hostWorld is this machine: its pid and clock, kill(pid, 0) for a live holder, an flock
// for the lock's guard, and execve for the command.
func hostWorld() world {
	home, _ := os.UserHomeDir() // "" leaves a ~/ path relative, which the run reports when it fails
	return world{pid: os.Getpid(), alive: alive, now: time.Now, home: home, guard: flockGuard, exec: execCommand}
}

// alive is whether pid names a live process: one this user may signal, or one it may not
// (EPERM), which is still a live holder.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// flockGuard holds an exclusive flock on path until release. The descriptor is
// close-on-exec, so the guard never reaches the command.
func flockGuard(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close() // ignored: the flock's error is the one returned
		return nil, err
	}
	return func() { _ = f.Close() }, nil // ignored: closing releases the flock, and a close error leaves nothing held
}

// execCommand replaces this process with the command, found on PATH as a shell would.
func execCommand(argv []string) error {
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	return syscall.Exec(path, argv, os.Environ())
}
