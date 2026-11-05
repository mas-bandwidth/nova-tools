//go:build linux

package main

import (
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Landlock does not path-check a unix-domain connect, so a command inside the wall could
// reach any same-uid socket: /proc/net/unix lists the paths. The only fence without a
// path check is to stop the command making a unix socket that can be pointed at a path.
// This seccomp-bpf filter refuses socket(AF_UNIX) and every socketpair(AF_UNIX) except
// SOCK_STREAM. A stream pair is connected to its twin: connect and sendto elsewhere fail
// with EISCONN, so it is allowed, and event loops (Python asyncio, Go, libuv) need one.
// A DGRAM pair can reconnect or sendto any datagram path, and a SEQPACKET pair was
// measured to sendto a datagram path too, so both are refused. It also refuses ptrace and
// io_uring (whose queue runs operations seccomp does not see) at any argument. The
// filter is inherited across fork and execve beside the Landlock ruleset.

const (
	// An action the kernel does not know is treated as a kill, so KILL_PROCESS is safe
	// on kernels older than 4.14 too.
	secRetKill       = 0x80000000 // SECCOMP_RET_KILL_PROCESS
	secRetErrnoEPERM = 0x00050000 | uint32(unix.EPERM)
	secRetAllow      = 0x7fff0000
	secModeFilter    = 1 // SECCOMP_SET_MODE_FILTER
	x32Bit           = 0x40000000
)

// classic-BPF opcodes.
const (
	bpfLdWAbs = 0x20 // BPF_LD | BPF_W | BPF_ABS
	bpfJeqK   = 0x15 // BPF_JMP | BPF_JEQ | BPF_K
	bpfJgeK   = 0x35 // BPF_JMP | BPF_JGE | BPF_K
	bpfJmpJA  = 0x05 // BPF_JMP | BPF_JA
	bpfAndK   = 0x54 // BPF_ALU | BPF_AND | BPF_K
	bpfRetK   = 0x06 // BPF_RET | BPF_K
)

// seccomp_data offsets; args are read by their low word (little-endian targets).
const (
	offNr   = 0
	offArch = 4
	offArg0 = 16
	offArg1 = 24
)

// nativeArch is the AUDIT_ARCH of this build, or 0 when there is no program for it.
func nativeArch() uint32 {
	switch runtime.GOARCH {
	case "amd64":
		return unix.AUDIT_ARCH_X86_64
	case "arm64":
		return unix.AUDIT_ARCH_AARCH64
	}
	return 0
}

// deniedSyscalls are refused with EPERM whatever their arguments.
func deniedSyscalls() []uint32 {
	return []uint32{unix.SYS_PTRACE, unix.SYS_IO_URING_SETUP, unix.SYS_IO_URING_ENTER, unix.SYS_IO_URING_REGISTER}
}

// insn is one instruction with symbolic jump targets; label names its own position.
// An empty target is the next instruction.
type insn struct {
	label  string
	code   uint16
	k      uint32
	jt, jf string
}

// assemble resolves the labels into the kernel's relative offsets.
func assemble(src []insn) []unix.SockFilter {
	at := map[string]int{}
	for i, in := range src {
		if in.label != "" {
			at[in.label] = i
		}
	}
	off := func(i int, label string) uint8 {
		if label == "" {
			return 0
		}
		return uint8(at[label] - i - 1)
	}
	out := make([]unix.SockFilter, len(src))
	for i, in := range src {
		out[i] = unix.SockFilter{Code: in.code, K: in.k, Jt: off(i, in.jt), Jf: off(i, in.jf)}
		if in.code == bpfJmpJA {
			out[i] = unix.SockFilter{Code: in.code, K: uint32(off(i, in.jt))}
		}
	}
	return out
}

// buildFilter assembles the program. With filterUnix false (--unix) the AF_UNIX rules
// are left out and only the guards and the denied syscalls remain. nil on an
// unsupported architecture.
func buildFilter(filterUnix bool) []unix.SockFilter {
	arch := nativeArch()
	if arch == 0 {
		return nil
	}
	p := []insn{
		{code: bpfLdWAbs, k: offArch},
		{code: bpfJeqK, k: arch, jf: "kill"},
		{code: bpfLdWAbs, k: offNr},
		// x32 syscalls carry bit 30 and pass the x86_64 arch check: refuse them all,
		// or each would be an unfiltered twin of a refused syscall.
		{code: bpfJgeK, k: x32Bit, jt: "kill"},
	}
	if filterUnix {
		p = append(p,
			insn{code: bpfJeqK, k: unix.SYS_SOCKET, jt: "socket"},
			insn{code: bpfJeqK, k: unix.SYS_SOCKETPAIR, jt: "pair"})
	}
	for _, nr := range deniedSyscalls() {
		p = append(p, insn{code: bpfJeqK, k: nr, jt: "eperm"})
	}
	p = append(p, insn{code: bpfJmpJA, jt: "allow"})
	if filterUnix {
		p = append(p,
			insn{label: "socket", code: bpfLdWAbs, k: offArg0},
			insn{code: bpfJeqK, k: unix.AF_UNIX, jt: "eperm", jf: "allow"},
			insn{label: "pair", code: bpfLdWAbs, k: offArg0},
			insn{code: bpfJeqK, k: unix.AF_UNIX, jf: "allow"},
			insn{code: bpfLdWAbs, k: offArg1},
			insn{code: bpfAndK, k: 0xf}, // SOCK_TYPE_MASK: drop SOCK_NONBLOCK and SOCK_CLOEXEC
			insn{code: bpfJeqK, k: unix.SOCK_STREAM, jt: "allow", jf: "eperm"})
	}
	return assemble(append(p,
		insn{label: "allow", code: bpfRetK, k: secRetAllow},
		insn{label: "eperm", code: bpfRetK, k: secRetErrnoEPERM},
		insn{label: "kill", code: bpfRetK, k: secRetKill}))
}

// seccompAvailable: a kernel with filter mode answers a null program with EFAULT.
func seccompAvailable() bool {
	if nativeArch() == 0 {
		return false
	}
	_, _, errno := syscall.Syscall(unix.SYS_SECCOMP, secModeFilter, 0, 0)
	return errno == syscall.EFAULT
}

// installSeccomp loads the filter onto the calling thread, which must stay locked
// through to the exec; no_new_privs must already be set.
func installSeccomp(filterUnix bool) error {
	filter := buildFilter(filterUnix)
	if filter == nil {
		return syscall.ENOSYS
	}
	prog := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	_, _, errno := syscall.Syscall(unix.SYS_SECCOMP, secModeFilter, 0, uintptr(unsafe.Pointer(&prog)))
	runtime.KeepAlive(filter)
	if errno != 0 {
		return errno
	}
	return nil
}
