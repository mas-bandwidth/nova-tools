//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package main

import (
	"cmp"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

// systemDir is the reservation directory when NOVA_RESERVE_DIR names none.
// /var/tmp survives a reboot and is shared by every user, so the stored
// capacity outlives a reboot and every caller on the machine shares one.
const systemDir = "/var/tmp/nova-reserve"

// openIn opens a file of the reservation directory, where any user may have
// put anything: it follows no symbolic link (O_NOFOLLOW), waits on no FIFO
// (O_NONBLOCK), and refuses what is not a regular file, read off the open
// descriptor and never off the path, or that has a second name. A file it may make (O_CREATE) is 0666
// whatever the maker's umask, or no other user could open it, lock files too.
func openIn(path string, flag int) (*os.File, error) {
	odd := refuse(2, "%s is not a regular file; nova-reserve opens no link or special file in its directory; run: sudo rm -f %s", path, shellQuote(path))
	f, err := os.OpenFile(path, flag|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o666)
	if err != nil {
		// O_NOFOLLOW's error for a link differs by OS; the refusal is the same
		if fi, lerr := os.Lstat(path); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			return nil, odd
		}
		return nil, err
	}
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		// ignored: nothing was written through it
		_ = f.Close()
		return nil, cmp.Or[error](err, odd)
	} else if n := uint64(fi.Sys().(*syscall.Stat_t).Nlink); n > 1 {
		// a second name is a file elsewhere; every file this tool makes has one
		// name (made or renamed here, never linked). ignored: nothing was written
		_ = f.Close()
		return nil, refuse(2, "%s has %d names (a hard link); nova-reserve opens no file in its directory that has another name; run: sudo rm -f %s", path, n, shellQuote(path))
	}
	if flag&os.O_CREATE != 0 {
		// ignored: only the file's owner may widen it, and its maker is the owner
		_ = f.Chmod(0o666)
	}
	return f, nil
}

// osLock is flock(2) on path. The file is opened read-only (flock needs no
// write access, so every user can lock a file another user made), and opened
// before it is created, which a sticky /tmp refuses for another user's file.
// Go opens it close-on-exec, so the command never inherits the lock.
func osLock(path string, exclusive bool) (func(), bool, error) {
	f, err := openIn(path, os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		f, err = openIn(path, os.O_RDONLY|os.O_CREATE)
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

// tie starts cmd with this OS's tie to nova-reserve (sysAttr, watchdog). Linux's
// Pdeathsig fires when the starting thread ends, so the thread stays locked to
// this goroutine, which waits for the command. A command in a
// group of its own gets the terminal when nova-reserve is its foreground, else
// reading it stops the command (SIGTTIN) with its slot held; untie gives it back.
func tie(cmd *exec.Cmd) (func(), error) {
	runtime.LockOSThread()
	cmd.SysProcAttr = sysAttr()
	cmd.SysProcAttr.Foreground = cmd.SysProcAttr.Setpgid && terminalGroup() == syscall.Getpgrp() // Ctty 0: stdin
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	pid := cmd.Process.Pid
	untie, err := watchdog(pid)
	if err != nil {
		// ignored: a command with no watchdog is not left running
		_ = cmd.Process.Kill()
		passTerminal(pid, syscall.Getpgrp())
		return nil, err
	}
	return func() { untie(); passTerminal(pid, syscall.Getpgrp()) }, nil
}

// terminalGroup is the foreground group of the terminal on stdin; -1 if none.
func terminalGroup() int {
	if pgrp, err := unix.IoctlGetInt(0, unix.TIOCGPGRP); err == nil {
		return pgrp
	}
	return -1
}

// passTerminal hands the terminal from group from to group to, if from has it.
// A background group may only with SIGTTOU ignored, or the kernel stops it.
func passTerminal(from, to int) {
	if terminalGroup() != from {
		return
	}
	signal.Ignore(syscall.SIGTTOU)
	defer signal.Reset(syscall.SIGTTOU)
	// ignored: a terminal that is gone has no foreground to hand over
	_ = unix.IoctlSetPointerInt(0, unix.TIOCSPGRP, to)
}

// wait waits for cmd. On its controlling terminal, foreground or not (a run
// can be put in the background while it waits for a slot), it relays job
// control: when the command stops (Ctrl-Z; SIGTTIN after bg), nova-reserve
// takes back the terminal it had and stops its own group, so the shell sees
// the job stop; at the shell's SIGCONT, it passes on the terminal if the
// shell gave it one (fg), and continues the command. tla/JobControl.tla.
func wait(cmd *exec.Cmd) (ws syscall.WaitStatus, err error) {
	if !cmd.SysProcAttr.Setpgid || terminalGroup() < 0 {
		if err = cmd.Wait(); cmd.ProcessState != nil {
			return cmd.ProcessState.Sys().(syscall.WaitStatus), nil
		}
		return ws, err
	}
	pid, self := cmd.Process.Pid, syscall.Getpgrp()
	for {
		if _, err = syscall.Wait4(pid, &ws, syscall.WUNTRACED, nil); err != syscall.EINTR && (err != nil || !ws.Stopped()) {
			return ws, err
		}
		if err == nil {
			passTerminal(pid, self)
			conts := make(chan os.Signal, 1) // Drain: no earlier SIGCONT
			signal.Notify(conts, syscall.SIGCONT)
			// ignored: stopping this process's own group cannot fail
			_ = syscall.Kill(0, syscall.SIGSTOP)
			<-conts // kill returns before the stop lands: OwnStopLandsFirst
			signal.Stop(conts)
			passTerminal(self, pid)
			// ignored: a group that has already ended needs no SIGCONT
			_ = syscall.Kill(-pid, syscall.SIGCONT)
		}
	}
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

// signalName is the signal's name, such as SIGKILL.
func signalName(s syscall.Signal) string { return unix.SignalName(s) }

// running says pid is a process: EPERM is another user's live process.
func running(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
