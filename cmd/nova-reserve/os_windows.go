//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// systemDir is the reservation directory when NOVA_RESERVE_DIR names none.
const systemDir = `C:\ProgramData\nova-reserve`

// osLock is LockFileEx on the first byte of path. The handle is not
// inheritable, so the command never holds the lock, and the kernel lets go of
// it when this process ends, however it ends.
func osLock(path string, exclusive bool) (func(), bool, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, false, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, false, err
	}
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	if err := windows.LockFileEx(h, flags, 0, 1, 0, &windows.Overlapped{}); err != nil {
		// ignored: a handle this process holds no lock on
		_ = windows.CloseHandle(h)
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() {
		// ignored: closing the handle releases the lock even if the unlock failed
		_ = windows.UnlockFileEx(h, 0, 1, 0, &windows.Overlapped{})
		_ = windows.CloseHandle(h)
	}, true, nil
}

// tie starts cmd in a job object that kills every process in it when its last
// handle closes: nova-reserve holds the only one, so the command ends with it.
func tie(cmd *exec.Cmd) (func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	// ignored: closing the job ends whatever of the command is left
	untie := func() { _ = windows.CloseHandle(job) }
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	if err == nil {
		err = cmd.Start()
	}
	if err == nil {
		var h windows.Handle
		if h, err = windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid)); err == nil {
			err = windows.AssignProcessToJobObject(job, h)
			// ignored: the job keeps its own reference to the process
			_ = windows.CloseHandle(h)
		}
		if err != nil {
			// ignored: a command that cannot be tied to the slot is not left running
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}
	if err != nil {
		untie()
		return nil, err
	}
	return untie, nil
}

// forward does nothing: Windows has no signal to pass; a console's Ctrl+C
// reaches the command directly.
func forward(*exec.Cmd, os.Signal) {}

// running says pid is a process that has not exited.
func running(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	// ignored: a handle only queried
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == 259 // STILL_ACTIVE
}
