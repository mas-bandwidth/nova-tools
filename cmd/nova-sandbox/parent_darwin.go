//go:build darwin

package main

import (
	"bytes"
	"fmt"
	"syscall"
	"unsafe"
)

// parentExecutable answers "what binary is pid?" for the ONE pid the probe's child cares
// about: its own parent. It is proc_pidpath(2) through the proc_info syscall and NOT
// `ps -o comm= -p`, because the child asks this question INSIDE the wall and ps is
// setgid kmem: measured on macOS 26 with this tool's own profile, `ps` inside the wall is
// `/bin/ps: Operation not permitted`, exit 126, while proc_pidpath answers with the
// parent's full path. A guard that only works outside the wall would be no guard at all.
//
// sandbox-exec execs the command IN PLACE (wrap_darwin.go), so there is no process
// between the tool and this child and the parent pid IS the tool's.
func parentExecutable(pid int) (string, error) {
	// proc_pidpath: callnum PROC_INFO_CALL_PIDINFO (2), flavor PROC_PIDPATHINFO (11).
	// The buffer must be PROC_PIDPATHINFO_MAXSIZE; a smaller one is EINVAL.
	const (
		sysProcInfo    = 336
		callPidInfo    = 2
		flavorPidPath  = 11
		pathInfoMaxLen = 4 * 1024
	)
	buf := make([]byte, pathInfoMaxLen)
	_, _, errno := syscall.Syscall6(sysProcInfo, callPidInfo, uintptr(pid), flavorPidPath, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if errno != 0 {
		return "", fmt.Errorf("proc_pidpath(%d): %w", pid, errno)
	}
	n := bytes.IndexByte(buf, 0)
	if n <= 0 {
		return "", fmt.Errorf("proc_pidpath(%d) named no path", pid)
	}
	return string(buf[:n]), nil
}
