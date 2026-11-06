package main

import (
	"errors"
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// checkKey refuses a key file that is not a regular file owned by this user whose
// access list allows only this user, SYSTEM and Administrators (Windows has no mode bits).
func checkKey(path string) error {
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() {
		return deny("no-key", "key file %s is absent or not a regular file", path)
	}
	if !private(path) {
		return deny("no-key", "key file %s must be owned by you and private to you; run: icacls %q /inheritance:r /grant:r %%USERNAME%%:F", path, path)
	}
	return nil
}

// private is whether path is owned by this user, SYSTEM or Administrators and its access list allows no one else.
func private(path string) bool {
	bad := false
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return bad
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return bad
	}
	me := u.User.Sid
	trusted := func(s *windows.SID) bool {
		return s.Equals(me) || s.IsWellKnown(windows.WinLocalSystemSid) || s.IsWellKnown(windows.WinBuiltinAdministratorsSid)
	}
	owner, _, err := sd.Owner()
	if err != nil || !trusted(owner) {
		return bad
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return bad
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(dacl, i, &ace) != nil {
			return bad
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !trusted((*windows.SID)(unsafe.Pointer(&ace.SidStart))) {
			return bad
		}
	}
	return true
}

// become runs argv and exits with its status: Windows has no exec(2).
func become(argv, env []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		return err
	}
	os.Exit(cmd.ProcessState.ExitCode())
	return nil
}

// isTerminal is whether f is a console; NUL and pipes are not.
func isTerminal(f *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(f.Fd()), &mode) == nil
}

// checkPrivate refuses a pin file or directory whose owner or access list is not the key file's: this user, SYSTEM
// and Administrators only.
func checkPrivate(path string, _ os.FileInfo) error {
	if !private(path) {
		return refuse("not private")
	}
	return nil
}
