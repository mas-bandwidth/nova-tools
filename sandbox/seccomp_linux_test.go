//go:build linux

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// data is the seccomp_data the interpreter evaluates against.
type data struct{ arch, nr, arg0, arg1 uint32 }

// verdict interprets the classic-BPF program the way the kernel does, for the opcodes
// the program uses, and returns its return value. A jump out of range or an unknown
// opcode fails the test: the kernel would reject that program.
func verdict(t *testing.T, p []unix.SockFilter, d data) uint32 {
	t.Helper()
	field := map[uint32]uint32{offArch: d.arch, offNr: d.nr, offArg0: d.arg0, offArg1: d.arg1}
	var a uint32
	for pc := 0; pc < len(p); {
		in := p[pc]
		next := pc + 1
		switch in.Code {
		case bpfLdWAbs:
			a = field[in.K]
		case bpfAndK:
			a &= in.K
		case bpfJeqK, bpfJgeK:
			hit := a == in.K
			if in.Code == bpfJgeK {
				hit = a >= in.K
			}
			if hit {
				next += int(in.Jt)
			} else {
				next += int(in.Jf)
			}
		case bpfJmpJA:
			next += int(in.K)
		case bpfRetK:
			return in.K
		default:
			t.Fatalf("unknown opcode %#x at %d", in.Code, pc)
		}
		require.Less(t, next, len(p), "jump out of range at %d", pc)
		pc = next
	}
	t.Fatal("ran off the end of the program")
	return 0
}

func TestSeccompFilter(t *testing.T) {
	t.Parallel()
	arch := nativeArch()
	if arch == 0 {
		t.Skip("no seccomp program for this architecture")
	}
	strict, open := buildFilter(true), buildFilter(false)
	for _, c := range []struct {
		name       string
		d          data
		strict, op uint32
	}{
		{"wrong arch", data{arch: arch ^ 1, nr: unix.SYS_GETPID}, secRetKill, secRetKill},
		{"x32 nr", data{arch: arch, nr: x32Bit | unix.SYS_SOCKET, arg0: unix.AF_UNIX}, secRetKill, secRetKill},
		{"getpid", data{arch: arch, nr: unix.SYS_GETPID}, secRetAllow, secRetAllow},
		{"socket unix", data{arch: arch, nr: unix.SYS_SOCKET, arg0: unix.AF_UNIX, arg1: unix.SOCK_STREAM}, secRetErrnoEPERM, secRetAllow},
		{"socket inet", data{arch: arch, nr: unix.SYS_SOCKET, arg0: unix.AF_INET, arg1: unix.SOCK_STREAM}, secRetAllow, secRetAllow},
		{"pair stream", data{arch: arch, nr: unix.SYS_SOCKETPAIR, arg0: unix.AF_UNIX, arg1: unix.SOCK_STREAM | unix.SOCK_CLOEXEC}, secRetAllow, secRetAllow},
		{"pair seqpacket", data{arch: arch, nr: unix.SYS_SOCKETPAIR, arg0: unix.AF_UNIX, arg1: unix.SOCK_SEQPACKET | unix.SOCK_NONBLOCK}, secRetErrnoEPERM, secRetAllow},
		{"pair dgram", data{arch: arch, nr: unix.SYS_SOCKETPAIR, arg0: unix.AF_UNIX, arg1: unix.SOCK_DGRAM}, secRetErrnoEPERM, secRetAllow},
		{"ptrace", data{arch: arch, nr: unix.SYS_PTRACE}, secRetErrnoEPERM, secRetErrnoEPERM},
		{"io_uring_setup", data{arch: arch, nr: unix.SYS_IO_URING_SETUP}, secRetErrnoEPERM, secRetErrnoEPERM},
	} {
		assert.Equal(t, c.strict, verdict(t, strict, c.d), "default: %s", c.name)
		assert.Equal(t, c.op, verdict(t, open, c.d), "--unix: %s", c.name)
	}
	for _, nr := range deniedSyscalls() {
		assert.Equal(t, uint32(secRetErrnoEPERM), verdict(t, strict, data{arch: arch, nr: nr}))
	}
}
