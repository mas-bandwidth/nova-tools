//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package main

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// systemDir is the default local state directory on a standalone machine.
const systemDir = "/var/tmp/nova-reserve"

// ensureDir creates path if absent and verifies it is a real directory,
// following no symbolic link.
func ensureDir(path, dirFrom, retryCmd string) error {
	if err := os.Mkdir(path, 0o777); err != nil && !errors.Is(err, fs.ErrExist) {
		parent := filepath.Dir(path)
		if _, perr := os.Stat(parent); errors.Is(perr, fs.ErrNotExist) {
			return refuse(2, "%s %s: parent %s does not exist; run: mkdir -p %s && %s",
				dirFrom, path, parent, shellQuote(parent), retryCmd)
		}
		if errors.Is(err, fs.ErrPermission) {
			return refuse(2, "%s %s: parent %s is not writable by this user; run: ls -ld %s",
				dirFrom, path, parent, shellQuote(parent))
		}
		return refuse(2, "%s %s: %v; run: ls -ld %s", dirFrom, path, cmp.Or(errors.Unwrap(err), err), shellQuote(parent))
	}
	d, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0)
	if fi, lerr := os.Lstat(path); err != nil && lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
		return refuse(2, "%s %s is a symbolic link; nova-reserve uses no link as its directory: give the directory it names, or remove the link; run: ls -ld %s",
			dirFrom, path, shellQuote(path))
	}
	if err == nil {
		fi, serr := d.Stat()
		if serr == nil && fi.IsDir() {
			// ignored: only the directory's owner may widen it; others share it as it is
			_ = d.Chmod(0o777 | os.ModeSticky)
		}
		// ignored: opened read-only to check and widen mode
		_ = d.Close()
		if serr != nil || !fi.IsDir() {
			err = syscall.ENOTDIR
		}
	}
	if errors.Is(err, syscall.ENOTDIR) {
		return refuse(2, "%s %s is a file, not a directory; run: ls -ld %s", dirFrom, path, shellQuote(path))
	}
	return nil
}

// openIn opens a regular file inside the reservation directory, following no
// symbolic link, waiting on no FIFO, and refusing any file with a second hard link.
func openIn(path string, flag int) (*os.File, error) {
	odd := refuse(2, "%s is not a regular file; nova-reserve opens no link or special file in its directory; run: sudo rm -f %s", path, shellQuote(path))
	f, err := os.OpenFile(path, flag|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o666)
	if err != nil {
		if fi, lerr := os.Lstat(path); lerr == nil && !fi.Mode().IsRegular() {
			return nil, odd
		}
		return nil, err
	}
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		// ignored: nothing was written through it
		_ = f.Close()
		return nil, cmp.Or[error](err, odd)
	} else if n := uint64(fi.Sys().(*syscall.Stat_t).Nlink); n > 1 {
		// ignored: nothing was written through it
		_ = f.Close()
		return nil, refuse(2, "%s has %d names (a hard link); nova-reserve opens no file in its directory that has another name; run: sudo rm -f %s", path, n, shellQuote(path))
	}
	if flag&os.O_CREATE != 0 {
		// ignored: only the file's owner may widen it, and its maker is the owner
		_ = f.Chmod(0o666)
	}
	return f, nil
}

// readRecord reads the first line of a regular file in the reservation directory.
func readRecord(path string) ([]byte, bool, error) {
	f, err := openIn(path, os.O_RDONLY)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	b, rerr := io.ReadAll(f)
	// ignored: opened read-only
	_ = f.Close()
	if rerr != nil {
		return nil, false, rerr
	}
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		b = b[:i]
	}
	return b, true, nil
}

// writeRecord writes a single-line record ending in '\n'.
func writeRecord(path string, line []byte, pid int) error {
	rec := append(bytes.TrimRight(line, "\n"), '\n')
	tmp := path + "." + strconv.Itoa(pid) + ".tmp"
	tf, err := openIn(tmp, os.O_RDWR|os.O_CREATE)
	if err == nil {
		_, werr := tf.WriteAt(rec, 0)
		werr = errors.Join(werr, tf.Truncate(int64(len(rec))), tf.Sync(), tf.Close())
		if werr == nil && os.Rename(tmp, path) == nil {
			return nil
		}
		// ignored: best-effort cleanup of temp file before fallback
		_ = os.Remove(tmp)
		if werr != nil {
			return werr
		}
	} else if odd := (*exitError)(nil); errors.As(err, &odd) {
		return err
	}
	f, err := openIn(path, os.O_RDWR)
	if errors.Is(err, fs.ErrNotExist) {
		f, err = openIn(path, os.O_RDWR|os.O_CREATE)
	}
	if err != nil {
		return err
	}
	_, werr := f.WriteAt(rec, 0)
	return errors.Join(werr, f.Truncate(int64(len(rec))), f.Sync(), f.Close())
}

// readPPIDChain walks up to 32 ancestor PIDs starting at pid's parent so nested
// nova-reserve invocations discover their enclosing local lease even when an
// intermediate process scrubbed environment variables.
func readPPIDChain(pid int) []int {
	var chain []int
	seen := map[int]bool{pid: true}
	cur := pid
	for range 32 {
		pp := parentPID(cur)
		if pp <= 1 || seen[pp] {
			break
		}
		seen[pp] = true
		chain = append(chain, pp)
		cur = pp
	}
	return chain
}

// tie starts cmd in its own process group with OS lifecycle ties (Pdeathsig, watchdog).
func tie(cmd *exec.Cmd, lower bool) (func(), error) {
	runtime.LockOSThread()
	if lower {
		// ignored: advisory priority lowering; restricted containers may forbid it
		_ = lowerPriority()
	}
	cmd.SysProcAttr = sysAttr()
	cmd.SysProcAttr.Foreground = cmd.SysProcAttr.Setpgid && terminalGroup() == syscall.Getpgrp()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	pid := cmd.Process.Pid
	untie, err := watchdog(pid)
	if err != nil {
		// ignored: a command with no watchdog is killed immediately
		_ = cmd.Process.Kill()
		passTerminal(pid, syscall.Getpgrp())
		return nil, err
	}
	return func() { untie(); passTerminal(pid, syscall.Getpgrp()) }, nil
}

func terminalGroup() int {
	if pgrp, err := unix.IoctlGetInt(0, unix.TIOCGPGRP); err == nil {
		return pgrp
	}
	return -1
}

func passTerminal(from, to int) {
	if terminalGroup() != from {
		return
	}
	signal.Ignore(syscall.SIGTTOU)
	defer signal.Reset(syscall.SIGTTOU)
	// ignored: a closed terminal has no foreground group
	_ = unix.IoctlSetPointerInt(0, unix.TIOCSPGRP, to)
}

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
			if sid, serr := unix.Getsid(0); serr == nil && sid == self {
				// session leader on the terminal (ssh -t): no shell above to resume us, so never self-stop
				passTerminal(self, pid)
				// ignored: a group that has already ended needs no SIGCONT
				_ = syscall.Kill(-pid, syscall.SIGCONT)
				continue
			}
			passTerminal(pid, self)
			conts := make(chan os.Signal, 1)
			signal.Notify(conts, syscall.SIGCONT)
			// ignored: stopping our own process group cannot fail
			_ = syscall.Kill(0, syscall.SIGSTOP)
			<-conts
			signal.Stop(conts)
			passTerminal(self, pid)
			// ignored: a group that has already exited needs no SIGCONT
			_ = syscall.Kill(-pid, syscall.SIGCONT)
		}
	}
}

func forward(cmd *exec.Cmd, s os.Signal) {
	pid := cmd.Process.Pid
	if cmd.SysProcAttr.Setpgid {
		pid = -pid
	}
	if sig, ok := s.(syscall.Signal); ok {
		// ignored: a process that has already exited needs no signal
		_ = syscall.Kill(pid, sig)
	}
}

func signalName(s syscall.Signal) string { return unix.SignalName(s) }

func running(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func runCommand(path string, argv, env []string, lower bool) int {
	cmd := exec.Command(path, argv[1:]...)
	cmd.Args[0] = argv[0]
	if len(env) > 0 {
		cmd.Env = env
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	untie, err := tie(cmd, lower)
	if err != nil {
		signal.Stop(sigs)
		fmt.Fprintln(os.Stderr, "nova-reserve:", err)
		return 126
	}
	defer untie()
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigs:
				forward(cmd, s)
			case <-done:
				return
			}
		}
	}()
	ws, err := wait(cmd)
	signal.Stop(sigs)
	close(done)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nova-reserve:", err)
		return 126
	}
	if ws.Signaled() {
		fmt.Fprintf(os.Stderr, "nova-reserve: %s ended by signal %s; exit %d\n", argv[0], signalName(ws.Signal()), 128+int(ws.Signal()))
		return 128 + int(ws.Signal())
	}
	return ws.ExitStatus()
}
