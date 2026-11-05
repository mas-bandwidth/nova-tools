//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// checkKey refuses a key file that is not a regular file of mode 0600 owned by this user.
func checkKey(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return refuse(2, "key file %s: %v", path, err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 || !ok || int(st.Uid) != os.Getuid() {
		return refuse(2, "key file %s must be a regular file of mode 0600 owned by you (it is %v); run: chmod 600 %s", path, fi.Mode(), path)
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
