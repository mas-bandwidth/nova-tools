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
	"unicode/utf8"

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
func checkPrivate(fi os.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() || fi.Mode().Perm()&0o022 != 0 {
		return refuse("not private")
	}
	return nil
}

// writable is nil when this user may make files in dir.
func writable(dir string) error { return unix.Access(dir, unix.W_OK) }

// readHidden reads one line from the terminal f with echo off, so a pasted value is never shown, and with the
// terminal's own line editing off: the kernel holds an edited line in a buffer far smaller than maxValue (MAX_CANON),
// so each byte is taken as it comes and readLine does the editing. Signals stay the terminal's (ISIG).
func readHidden(f *os.File) ([]byte, error) {
	fd := int(f.Fd())
	old, err := unix.IoctlGetTermios(fd, getTermios)
	if err != nil {
		return nil, err
	}
	quiet := *old
	quiet.Lflag &^= unix.ECHO | unix.ICANON
	quiet.Lflag |= unix.ISIG
	quiet.Cc[unix.VMIN], quiet.Cc[unix.VTIME] = 1, 0 // a read waits for one byte, with no timer
	sig := make(chan os.Signal, 1)                   // a signal runs no defer: the terminal is put back, then the signal ends the process as it would have
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGHUP)
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
	return readLine(f, old.Cc[unix.VERASE], old.Cc[unix.VKILL], old.Cc[unix.VEOF])
}

// readLine reads one line a byte at a time and edits it as the terminal would have: CR or LF ends it, eof ends it
// too (and is the end of input on an empty line), erase takes back the last character and kill the whole line. It
// reads to the line's end whatever the length, so nothing pasted is left for the shell, and keeps at most maxValue+1
// bytes: one over what set accepts, so a line that is too long is refused, never cut. Erase does not shorten a line
// that is over: only kill starts it again.
func readLine(r io.Reader, erase, kill, eof byte) ([]byte, error) {
	var line []byte
	for b := make([]byte, 1); ; {
		if n, err := r.Read(b); n == 0 || err != nil {
			if len(line) > 0 {
				return line, nil
			}
			return nil, cmp.Or(err, io.EOF)
		}
		switch c := b[0]; {
		case c == '\n' || c == '\r' || c == eof && len(line) > 0:
			return line, nil
		case c == eof:
			return nil, io.EOF
		case c == kill:
			line = line[:0]
		case c == erase && len(line) <= maxValue:
			_, size := utf8.DecodeLastRune(line)
			line = line[:len(line)-size]
		case c != erase && len(line) <= maxValue:
			line = append(line, c)
		}
	}
}
