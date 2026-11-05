//go:build linux

package main

import (
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The program is bpf.go's; this file only finds out whether the kernel takes one and
// installs it.

const secModeFilter = 1 // SECCOMP_SET_MODE_FILTER

// nativeArch is the syscall table of this build, or false when there is no program
// for it.
func nativeArch() (archNums, bool) {
	a, ok := archTable[runtime.GOARCH]
	return a, ok
}

// seccompAvailable: a kernel with filter mode answers a null program with EFAULT.
func seccompAvailable() bool {
	if _, ok := nativeArch(); !ok {
		return false
	}
	_, _, errno := syscall.Syscall(unix.SYS_SECCOMP, secModeFilter, 0, 0)
	return errno == syscall.EFAULT
}

// installSeccomp loads the filter onto the calling thread, which must stay locked
// through to the exec; no_new_privs must already be set.
func installSeccomp(o filterOpts) error {
	arch, ok := nativeArch()
	if !ok {
		return syscall.ENOSYS
	}
	filter, _ := buildFilter(arch, o)
	prog := unix.SockFprog{Len: uint16(len(filter)), Filter: (*unix.SockFilter)(unsafe.Pointer(&filter[0]))}
	_, _, errno := syscall.Syscall(unix.SYS_SECCOMP, secModeFilter, 0, uintptr(unsafe.Pointer(&prog)))
	runtime.KeepAlive(filter)
	if errno != 0 {
		return errno
	}
	return nil
}
