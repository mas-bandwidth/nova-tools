//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
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
