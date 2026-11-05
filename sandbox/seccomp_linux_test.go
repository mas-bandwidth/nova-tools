//go:build linux

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// wellFormed checks that every jump lands inside the program and that the last
// instruction is a return: a malformed BPF program is rejected by the kernel, and the
// jump arithmetic is hand-computed, so this is the guard on that arithmetic.
func wellFormed(t *testing.T, p []unix.SockFilter) {
	t.Helper()
	require.NotEmpty(t, p)
	for i, in := range p {
		if in.Code == bpfJeqK {
			assert.Less(t, i+1+int(in.Jt), len(p), "jt at %d out of range", i)
			assert.Less(t, i+1+int(in.Jf), len(p), "jf at %d out of range", i)
		}
		if in.Code == bpfJmpJA {
			assert.Less(t, i+1+int(in.K), len(p), "ja at %d out of range", i)
		}
	}
	assert.EqualValues(t, bpfRetK, p[len(p)-1].Code, "the program ends in a return")
}

func TestBuildFilterWellFormed(t *testing.T) {
	t.Parallel()
	if nativeArch() == 0 {
		t.Skip("no seccomp program for this architecture")
	}
	deny := buildFilter(true)
	wellFormed(t, deny)
	allow := buildFilter(false)
	wellFormed(t, allow)
	assert.Greater(t, len(deny), len(allow), "the default filter also screens socket domains")

	// Both filters refuse ptrace and io_uring whatever their arguments.
	for _, nr := range deniedSyscalls() {
		assert.True(t, filterRefuses(deny, nr, 0), "default refuses %d", nr)
		assert.True(t, filterRefuses(allow, nr, 0), "--unix refuses %d", nr)
	}
	// socket(AF_UNIX) is refused by default and allowed under --unix; socket(AF_INET)
	// passes either way.
	assert.True(t, filterRefuses(deny, unix.SYS_SOCKET, unix.AF_UNIX))
	assert.False(t, filterRefuses(deny, unix.SYS_SOCKET, unix.AF_INET))
	assert.False(t, filterRefuses(allow, unix.SYS_SOCKET, unix.AF_UNIX))
}

// filterRefuses interprets the classic-BPF program against a seccomp_data of the given
// syscall and first argument, and reports whether the verdict is the EPERM return. It
// mirrors the kernel's evaluation closely enough to check the program's logic.
func filterRefuses(p []unix.SockFilter, nr, arg0 uint32) bool {
	field := func(off uint32) uint32 {
		switch off {
		case offArch:
			return nativeArch()
		case offNr:
			return nr
		case offArg0:
			return arg0
		}
		return 0
	}
	var a uint32
	for pc := 0; pc < len(p); {
		in := p[pc]
		switch in.Code {
		case bpfLdWAbs:
			a = field(in.K)
			pc++
		case bpfJeqK:
			if a == in.K {
				pc += 1 + int(in.Jt)
			} else {
				pc += 1 + int(in.Jf)
			}
		case bpfJmpJA:
			pc += 1 + int(in.K)
		case bpfRetK:
			return in.K == secRetErrnoEPERM
		default:
			return false
		}
	}
	return false
}
