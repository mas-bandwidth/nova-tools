//go:build !windows

package main

import (
	"cmp"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

// checkKey refuses a key file that is not a regular file of mode 0600 owned by this user.
func checkKey(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return deny("no-key", "key file %s does not exist; a seat's key is made once, on its machine: age-keygen -o %s && chmod 600 %s", path, path, path)
	}
	if err != nil {
		return deny("no-key", "key file %s: %v", path, err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 || !ok || int(st.Uid) != os.Getuid() {
		return deny("no-key", "key file %s must be a regular file of mode 0600 owned by you (it is %v); run: chmod 600 %s", path, fi.Mode(), path)
	}
	return nil
}

// become replaces this process with argv, so the command's exit status is exec's.
// No core file: a crash must not write the values to disk.
func become(argv, env []string) error {
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{}); err != nil {
		return err
	}
	return syscall.Exec(bin, argv, env)
}

// isTerminal is whether f is a terminal; /dev/null and pipes are not.
func isTerminal(f *os.File) bool {
	_, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	return err == nil
}

// checkPrivate refuses a pin file or directory that is not this user's, or that its group or others can write.
func checkPrivate(_ string, fi os.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() || fi.Mode().Perm()&0o022 != 0 {
		return refuse("not private")
	}
	return nil
}

// readHidden reads one line from the terminal f with echo off, so a pasted value is never shown.
func readHidden(f *os.File) ([]byte, error) {
	fd := int(f.Fd())
	old, err := unix.IoctlGetTermios(fd, getTermios)
	if err != nil {
		return nil, err
	}
	quiet := *old
	quiet.Lflag &^= unix.ECHO
	quiet.Lflag |= unix.ICANON | unix.ISIG
	sig := make(chan os.Signal, 1) // Ctrl-C runs no defer: echo is put back, then the signal ends the process as it would have
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	if err := unix.IoctlSetTermios(fd, setTermios, &quiet); err != nil {
		return nil, err
	}
	defer func() { _ = unix.IoctlSetTermios(fd, setTermios, old) }()
	go func() {
		s := <-sig
		_ = unix.IoctlSetTermios(fd, setTermios, old) // ignored: the process ends next either way
		signal.Reset(s)
		_ = syscall.Kill(os.Getpid(), s.(syscall.Signal)) // ignored: a process may always signal itself
	}()
	var line []byte
	b := make([]byte, 1)
	for len(line) <= maxValue+2 {
		n, err := f.Read(b)
		if n == 0 || err != nil {
			if len(line) > 0 {
				return line, nil
			}
			return nil, cmp.Or(err, io.EOF)
		}
		if b[0] == '\n' || b[0] == '\r' {
			return line, nil
		}
		line = append(line, b[0])
	}
	return line, nil
}
