//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows has no process group to own and no manager's TERM to answer: the deadline and the
// tree kill (swarm.KillGroup, taskkill /T) end a run, and the Job Object that holds this
// process (bindChildrenToThisProcess) ends every child left when this process stops or
// crashes.
func ownChildGroup(cmd *exec.Cmd) {}

func ownLaneSession(cmd *exec.Cmd) {}

func nativeTermCh() chan os.Signal { return make(chan os.Signal) }

func stopNativeTerm(ch chan os.Signal) {}

// lowersEveryChild says the child is lowered after its start whatever the wall: Windows has
// no nice to put in a card's gate line, so native puts the child below normal itself.
const lowersEveryChild = true

// lowerChildPriority puts the child at BELOW_NORMAL_PRIORITY_CLASS (pgid is its pid; there
// is no group). A process the child creates after it inherits the class, since a child of a
// BELOW_NORMAL process starts BELOW_NORMAL unless it asks otherwise (CreateProcess, the
// priority class flags).
func lowerChildPriority(pgid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_INFORMATION, false, uint32(pgid))
	if err != nil {
		return fmt.Errorf("OpenProcess(%d): %w", pgid, err)
	}
	defer windows.CloseHandle(h) // ignored: closing a handle after the call that used it
	if err := windows.SetPriorityClass(h, windows.BELOW_NORMAL_PRIORITY_CLASS); err != nil {
		return fmt.Errorf("SetPriorityClass(%d, BELOW_NORMAL_PRIORITY_CLASS): %w", pgid, err)
	}
	return nil
}

// childJob is the Job Object this process is in for its life (bindChildrenToThisProcess).
// Its one handle is never closed by hand: the kernel closes it when the process ends, by a
// stop or a crash alike, and JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE then ends every process
// still in the job, the children and their children. Kept so the handle is never collected.
var childJob windows.Handle

// newKillOnCloseJob makes a Job Object whose base limit is JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
// and nothing that permits breakaway, so no process in it can leave it and every one ends when
// the last handle closes. The handle is not inheritable, so no child holds the job open.
func newKillOnCloseJob() (windows.Handle, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("CreateJobObjectW: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(h) // ignored: a close on the failure path; the set's error is the one returned
		return 0, fmt.Errorf("SetInformationJobObject(JobObjectExtendedLimitInformation): %w", err)
	}
	return h, nil
}

// bindChildrenToThisProcess puts this process in a new kill-on-close Job Object, so every
// child it starts afterwards is in the job from its creation (a child inherits its parent's
// job: no window between a child's start and its assignment), and a stop or a crash of this
// process ends them all. Windows 8 and later nest jobs, so a nova-swarm already in a job (a
// member's native child, a scheduled task's) makes its own inside it.
func bindChildrenToThisProcess() error {
	if childJob != 0 {
		return nil // bound once for the process's life
	}
	h, err := newKillOnCloseJob()
	if err != nil {
		return err
	}
	if err := windows.AssignProcessToJobObject(h, windows.CurrentProcess()); err != nil {
		windows.CloseHandle(h) // ignored: a close on the failure path; the assign's error is the one returned
		return fmt.Errorf("AssignProcessToJobObject(this process): %w", err)
	}
	childJob = h
	return nil
}
