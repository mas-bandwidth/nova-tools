//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package main

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

// systemDir is the reservation directory when NOVA_RESERVE_DIR names none.
// /var/tmp survives a reboot and is shared by every user, so the stored
// capacity outlives a reboot and every caller on the machine shares one.
const systemDir = "/var/tmp/nova-reserve"

// osLock is flock(2) on path. The file is opened read-only (flock needs no
// write access, so every user can lock a file another user made), and opened
// before it is created, which a sticky /tmp refuses for another user's file.
// Go opens it close-on-exec, so the command never inherits the lock.
func osLock(path string, exclusive bool) (func(), bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		f, err = os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o666)
	}
	if err != nil {
		return nil, false, err
	}
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB); err != nil {
		// ignored: a file this process holds no lock on
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, err
	}
	// ignored: closing the descriptor is the release, and cannot fail to release
	return func() { _ = f.Close() }, true, nil
}

// tie starts cmd with this OS's tie to nova-reserve (sysAttr, watchdog). On Linux
// that is Pdeathsig, which fires when the starting thread ends, so the thread
// stays locked to this goroutine, which waits for the command.
func tie(cmd *exec.Cmd) (func(), error) {
	runtime.LockOSThread()
	cmd.SysProcAttr = sysAttr()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	untie, err := watchdog(cmd.Process.Pid)
	if err != nil {
		// ignored: a command with no watchdog is not left running
		_ = cmd.Process.Kill()
		return nil, err
	}
	return untie, nil
}

// forward passes an interrupt to the command, or to its process group.
func forward(cmd *exec.Cmd, s os.Signal) {
	pid := cmd.Process.Pid
	if cmd.SysProcAttr.Setpgid {
		pid = -pid
	}
	if sig, ok := s.(syscall.Signal); ok {
		// ignored: a process that has already ended needs no signal
		_ = syscall.Kill(pid, sig)
	}
}

// running says pid is a process: EPERM is another user's live process.
func running(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
