//go:build linux

package main

import (
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Landlock does not path-check a unix-domain connect, so a command inside the wall can
// still reach any same-uid socket: it reads /proc/net/unix for the paths and the fences
// on the environment are no help. The only way to close it without a path check is to
// stop the command creating an AF_UNIX socket at all. This seccomp-bpf filter does that,
// and also refuses the two syscall families that would let the command act outside the
// filter's view: io_uring (its submission queue runs operations the kernel does not
// re-check through seccomp) and ptrace (attaching to another process). The filter is
// inherited across fork and execve beside the Landlock ruleset.

// seccomp return values (linux/seccomp.h). KILL_THREAD is the widest action every kernel
// with seccomp-bpf supports; it is used only for a syscall from the wrong architecture.
const (
	secRetKillThread = 0x00000000
	secRetErrnoEPERM = 0x00050000 | uint32(unix.EPERM) // SECCOMP_RET_ERRNO | EPERM
	secRetAllow      = 0x7fff0000
	secModeFilter    = 1 // SECCOMP_SET_MODE_FILTER
)

// classic-BPF opcodes for the seccomp program.
const (
	bpfLdWAbs = 0x20 // BPF_LD | BPF_W | BPF_ABS
	bpfJeqK   = 0x15 // BPF_JMP | BPF_JEQ | BPF_K
	bpfJmpJA  = 0x05 // BPF_JMP | BPF_JA
	bpfRetK   = 0x06 // BPF_RET | BPF_K
)

// seccomp_data offsets: a load at these absolute byte offsets reads the field.
const (
	offNr   = 0
	offArch = 4
	offArg0 = 16 // args[0], low word first on a little-endian target
)

// nativeArch is the AUDIT_ARCH of this build, or 0 when the architecture is one this
// tool has no seccomp program for (the wall then needs --unix).
func nativeArch() uint32 {
	switch runtime.GOARCH {
	case "amd64":
		return unix.AUDIT_ARCH_X86_64
	case "arm64":
		return unix.AUDIT_ARCH_AARCH64
	}
	return 0
}

// deniedSyscalls are refused with EPERM whatever their arguments: ptrace and the three
// io_uring entry points. The numbers come from x/sys for this build's architecture, and
// the filter's architecture guard rejects any syscall that does not match it.
func deniedSyscalls() []uint32 {
	return []uint32{unix.SYS_PTRACE, unix.SYS_IO_URING_SETUP, unix.SYS_IO_URING_ENTER, unix.SYS_IO_URING_REGISTER}
}

// buildFilter assembles the seccomp program. When filterUnix is true, socket(2) and
// socketpair(2) with domain AF_UNIX are refused; when false (--unix) they pass, and
// only the denied syscalls above are refused. It returns nil on an unsupported
// architecture.
func buildFilter(filterUnix bool) []unix.SockFilter {
	arch := nativeArch()
	if arch == 0 {
		return nil
	}
	stmt := func(code uint16, k uint32) unix.SockFilter { return unix.SockFilter{Code: code, K: k} }
	jeq := func(k uint32, jt, jf uint8) unix.SockFilter {
		return unix.SockFilter{Code: bpfJeqK, Jt: jt, Jf: jf, K: k}
	}

	denied := deniedSyscalls()
	nD := len(denied)
	var p []unix.SockFilter
	if filterUnix {
		// Layout (indices): 0 LD arch; 1 JEQ arch; 2 LD nr; 3 socket; 4 socketpair;
		// 5..5+nD-1 denied; jmpIdx JMP allow; sockLD; sockJEQ; then allow, eperm, kill.
		jmpIdx := 5 + nD
		sockLD := jmpIdx + 1
		sockJEQ := sockLD + 1
		iAllow := sockJEQ + 1
		iEperm := iAllow + 1
		iKill := iEperm + 1
		off := func(from, to int) uint8 { return uint8(to - (from + 1)) }
		p = append(p,
			stmt(bpfLdWAbs, offArch),                    // 0
			jeq(arch, 0, off(1, iKill)),                 // 1: wrong arch -> kill
			stmt(bpfLdWAbs, offNr),                      // 2
			jeq(unix.SYS_SOCKET, off(3, sockLD), 0),     // 3
			jeq(unix.SYS_SOCKETPAIR, off(4, sockLD), 0), // 4
		)
		for i, nr := range denied {
			p = append(p, jeq(nr, off(5+i, iEperm), 0))
		}
		p = append(p,
			unix.SockFilter{Code: bpfJmpJA, K: uint32(off(jmpIdx, iAllow))}, // JMP allow
			stmt(bpfLdWAbs, offArg0), // sockLD: load domain
			jeq(unix.AF_UNIX, off(sockJEQ, iEperm), off(sockJEQ, iAllow)), // sockJEQ
			stmt(bpfRetK, secRetAllow),                                    // allow
			stmt(bpfRetK, secRetErrnoEPERM),                               // eperm
			stmt(bpfRetK, secRetKillThread),                               // kill
		)
		return p
	}
	// --unix: AF_UNIX stays open; only the denied syscalls and the arch guard remain.
	iAllow := 3 + nD
	iEperm := iAllow + 1
	iKill := iEperm + 1
	off := func(from, to int) uint8 { return uint8(to - (from + 1)) }
	p = append(p,
		stmt(bpfLdWAbs, offArch),    // 0
		jeq(arch, 0, off(1, iKill)), // 1
		stmt(bpfLdWAbs, offNr),      // 2
	)
	for i, nr := range denied {
		p = append(p, jeq(nr, off(3+i, iEperm), 0))
	}
	p = append(p,
		stmt(bpfRetK, secRetAllow),      // allow (fall-through)
		stmt(bpfRetK, secRetErrnoEPERM), // eperm
		stmt(bpfRetK, secRetKillThread), // kill
	)
	return p
}

// seccompAvailable reports whether this kernel takes a seccomp filter. It calls seccomp
// with a null program: a kernel that supports filter mode rejects that with EFAULT,
// while one without the syscall or the mode answers ENOSYS or EINVAL.
func seccompAvailable() bool {
	if nativeArch() == 0 {
		return false
	}
	_, _, errno := syscall.Syscall(unix.SYS_SECCOMP, secModeFilter, 0, 0)
	return errno == syscall.EFAULT
}

// installSeccomp loads the filter onto the calling thread. no_new_privs must already be
// set (execWall sets it for Landlock). The thread must stay locked through to exec.
func installSeccomp(filterUnix bool) error {
	filter := buildFilter(filterUnix)
	if filter == nil {
		return syscall.ENOSYS
	}
	prog := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	if _, _, errno := syscall.Syscall(unix.SYS_SECCOMP, secModeFilter, 0, uintptr(unsafe.Pointer(&prog))); errno != 0 {
		runtime.KeepAlive(filter)
		return errno
	}
	runtime.KeepAlive(filter)
	return nil
}
