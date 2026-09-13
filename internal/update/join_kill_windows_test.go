//go:build windows

package update

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

var (
	kernel32Dll            = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW   = kernel32Dll.NewProc("CreateJobObjectW")
	procAssignProcessToJob = kernel32Dll.NewProc("AssignProcessToJobObject")
	procTerminateJobObject = kernel32Dll.NewProc("TerminateJobObject")
	procQueryInfoJobObject = kernel32Dll.NewProc("QueryInformationJobObject")
)

type jobState struct {
	handle   syscall.Handle
	assigned bool
}

var (
	jobMu  sync.Mutex
	jobMap = make(map[*exec.Cmd]*jobState)
)

func setGroup(c *exec.Cmd) {
	// Create an anonymous Job Object so all descendant processes (git, etc.)
	// spawned by this command are tracked together.
	h, _, err := procCreateJobObjectW.Call(0, 0)
	if h == 0 {
		panic(fmt.Sprintf("CreateJobObjectW failed: %v", err))
	}
	jobMu.Lock()
	jobMap[c] = &jobState{handle: syscall.Handle(h)}
	jobMu.Unlock()
}

const (
	processTerminate = 0x0001
	processSetQuota  = 0x0100
)

func assignGroup(c *exec.Cmd) error {
	if c.Process == nil {
		return fmt.Errorf("assignGroup: process not started")
	}
	jobMu.Lock()
	st, ok := jobMap[c]
	jobMu.Unlock()
	if !ok || st == nil || st.handle == 0 {
		return fmt.Errorf("assignGroup: no job object registered for cmd")
	}
	if st.assigned {
		return nil
	}
	hProc, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(c.Process.Pid))
	if err != nil {
		return fmt.Errorf("OpenProcess(pid=%d) failed: %w", c.Process.Pid, err)
	}
	defer syscall.CloseHandle(hProc)
	r, _, callErr := procAssignProcessToJob.Call(uintptr(st.handle), uintptr(hProc))
	if r == 0 {
		return fmt.Errorf("AssignProcessToJobObject(pid=%d) failed: %w", c.Process.Pid, callErr)
	}
	st.assigned = true
	return nil
}

func killGroup(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	jobMu.Lock()
	st, ok := jobMap[c]
	jobMu.Unlock()
	if ok && st != nil && st.handle != 0 && st.assigned {
		r, _, err := procTerminateJobObject.Call(uintptr(st.handle), 1)
		if r != 0 {
			return nil
		}
		_ = err
	}
	return c.Process.Kill()
}

func groupGone(c *exec.Cmd) bool {
	if c.Process == nil {
		return true
	}
	jobMu.Lock()
	st, ok := jobMap[c]
	jobMu.Unlock()
	if !ok || st == nil || st.handle == 0 || !st.assigned {
		// Fallback check on root process PID only if no job was assigned
		hProc, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION|syscall.SYNCHRONIZE, false, uint32(c.Process.Pid))
		if err != nil {
			return true
		}
		defer syscall.CloseHandle(hProc)
		event, err := syscall.WaitForSingleObject(hProc, 0)
		return err == nil && event != syscall.WAIT_TIMEOUT
	}

	type jobObjectBasicAccountingInfo struct {
		TotalUserTime             int64
		TotalKernelTime           int64
		ThisPeriodTotalUserTime   int64
		ThisPeriodTotalKernelTime int64
		TotalPageFaultCount       uint32
		TotalProcesses            uint32
		ActiveProcesses           uint32
		TotalTerminatedProcesses  uint32
	}
	var info jobObjectBasicAccountingInfo
	var retLen uint32
	r, _, _ := procQueryInfoJobObject.Call(
		uintptr(st.handle),
		1, // JobObjectBasicAccountingInformation
		uintptr(unsafe.Pointer(&info)),
		uintptr(unsafe.Sizeof(info)),
		uintptr(unsafe.Pointer(&retLen)),
	)
	if r != 0 {
		// Prove that at least one process was tracked, and all active processes are gone.
		// If info.TotalProcesses == 0, the job was never populated and cannot claim gone.
		if info.TotalProcesses > 0 && info.ActiveProcesses == 0 {
			jobMu.Lock()
			delete(jobMap, c)
			jobMu.Unlock()
			syscall.CloseHandle(st.handle)
			return true
		}
		return false
	}
	return false
}
