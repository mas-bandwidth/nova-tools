//go:build windows

package update

import (
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

var (
	jobMu  sync.Mutex
	jobMap = make(map[*exec.Cmd]syscall.Handle)
)

func setGroup(c *exec.Cmd) {
	// Create an anonymous Job Object so all descendant processes (git, etc.)
	// spawned by this command are tracked together.
	h, _, _ := procCreateJobObjectW.Call(0, 0)
	if h != 0 {
		jobMu.Lock()
		jobMap[c] = syscall.Handle(h)
		jobMu.Unlock()
	}
}

func assignGroup(c *exec.Cmd) {
	ensureJobAssigned(c)
}

const (
	processTerminate = 0x0001
	processSetQuota  = 0x0100
)

func ensureJobAssigned(c *exec.Cmd) syscall.Handle {
	jobMu.Lock()
	defer jobMu.Unlock()
	h, ok := jobMap[c]
	if !ok || h == 0 {
		return 0
	}
	if c.Process != nil {
		hProc, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(c.Process.Pid))
		if err == nil {
			defer syscall.CloseHandle(hProc)
			procAssignProcessToJob.Call(uintptr(h), uintptr(hProc))
		}
	}
	return h
}

func killGroup(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	h := ensureJobAssigned(c)
	if h != 0 {
		r, _, err := procTerminateJobObject.Call(uintptr(h), 1)
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
	h, ok := jobMap[c]
	jobMu.Unlock()
	if ok && h != 0 {
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
			uintptr(h),
			1, // JobObjectBasicAccountingInformation
			uintptr(unsafe.Pointer(&info)),
			uintptr(unsafe.Sizeof(info)),
			uintptr(unsafe.Pointer(&retLen)),
		)
		if r != 0 {
			if info.ActiveProcesses == 0 {
				jobMu.Lock()
				delete(jobMap, c)
				jobMu.Unlock()
				syscall.CloseHandle(h)
				return true
			}
			return false
		}
	}
	// Fallback check on root process PID
	hProc, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION|syscall.SYNCHRONIZE, false, uint32(c.Process.Pid))
	if err != nil {
		return true
	}
	defer syscall.CloseHandle(hProc)
	event, err := syscall.WaitForSingleObject(hProc, 0)
	return err == nil && event != syscall.WAIT_TIMEOUT
}
