//go:build darwin && (amd64 || arm64)

package life

import (
	"encoding/binary"
	"fmt"
	"syscall"
	"unsafe"
)

// ProbeProcess reads kern.proc.pid through the numeric CTL_KERN,
// KERN_PROC, KERN_PROC_PID MIB; the pid suffix is not a named sysctl.
// On 64-bit Darwin, sys/proc.h exports timeval at 0, status at 36 and pid
// at 40 in extern_proc. Validate pid and timeval before trusting this ABI.
// No subprocess, command-line scan or signal to the process is used.
func ProbeProcess(pid int) ProcessSample {
	if pid <= 0 {
		return ProcessSample{Err: fmt.Errorf("invalid pid")}
	}
	mib := [4]int32{1, 14, 1, int32(pid)}
	var raw [4096]byte
	n := uintptr(len(raw))
	_, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL, uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)), uintptr(unsafe.Pointer(&raw[0])), uintptr(unsafe.Pointer(&n)), 0, 0)
	var err error
	if errno != 0 {
		err = errno
	}
	if err == syscall.ESRCH {
		return ProcessSample{Absent: true}
	}
	if err != nil {
		return ProcessSample{Err: err}
	}
	if n == 0 {
		return ProcessSample{Absent: true}
	}
	if n < 44 || n > uintptr(len(raw)) {
		return ProcessSample{Err: fmt.Errorf("short kern.proc.pid record")}
	}
	b := raw[:n]
	sec := binary.LittleEndian.Uint64(b[0:8])
	usec := binary.LittleEndian.Uint32(b[8:12])
	if int(binary.LittleEndian.Uint32(b[40:44])) != pid || sec == 0 || usec >= 1000000 {
		return ProcessSample{Err: fmt.Errorf("unrecognized kern.proc.pid identity")}
	}
	if b[36] == 5 {
		return ProcessSample{Absent: true}
	} // SZOMB, sys/proc.h
	return ProcessSample{Start: fmt.Sprintf("%d.%06d", sec, usec)}
}
