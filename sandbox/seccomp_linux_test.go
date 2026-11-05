//go:build linux

package main

import (
	"runtime"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// TestNativeArchMatchesTheKernelHeaders ties bpf.go's written-out numbers to x/sys/unix
// for the architecture this test is built for; CI builds amd64 and arm64.
func TestNativeArchMatchesTheKernelHeaders(t *testing.T) {
	t.Parallel()
	a, ok := nativeArch()
	if !ok {
		t.Skip("no seccomp program for this architecture")
	}
	want := map[string]uint32{"amd64": unix.AUDIT_ARCH_X86_64, "arm64": unix.AUDIT_ARCH_AARCH64}
	require.Contains(t, want, runtime.GOARCH)
	assert.Equal(t, want[runtime.GOARCH], a.audit)
	assert.EqualValues(t, unix.SYS_SOCKET, a.socket)
	assert.EqualValues(t, unix.SYS_SOCKETPAIR, a.socketpair)
	assert.EqualValues(t, unix.SYS_PTRACE, a.ptrace)
	assert.EqualValues(t, unix.SYS_IO_URING_SETUP, a.uringSetup)
	assert.EqualValues(t, unix.SYS_IO_URING_ENTER, a.uringEnter)
	assert.EqualValues(t, unix.SYS_IO_URING_REGISTER, a.uringRegister)
	assert.EqualValues(t, unix.SYS_SENDTO, a.sendto)
	assert.EqualValues(t, unix.SYS_SENDMSG, a.sendmsg)
	assert.EqualValues(t, unix.SYS_SENDMMSG, a.sendmmsg)
	assert.EqualValues(t, unix.SYS_LISTEN, a.listen)
	assert.EqualValues(t, unix.SYS_ACCEPT, a.accept)
	assert.EqualValues(t, unix.SYS_ACCEPT4, a.accept4)
	assert.EqualValues(t, unix.MSG_FASTOPEN, msgFastopen)
	assert.EqualValues(t, unix.NETLINK_ROUTE, netlinkRoute)
	assert.EqualValues(t, unix.AF_UNIX, afUnix)
	assert.EqualValues(t, unix.AF_INET, afInet)
	assert.EqualValues(t, unix.AF_INET6, afInet6)
	assert.EqualValues(t, unix.AF_NETLINK, afNetlink)
	assert.EqualValues(t, unix.SOCK_STREAM, sockStream)
	assert.EqualValues(t, unix.IPPROTO_TCP, ipprotoTCP)
	assert.EqualValues(t, unix.EPERM, secRetErrnoEPERM&0xffff)
	assert.Equal(t, unsafe.Sizeof(unix.SockFilter{}), unsafe.Sizeof(sockFilter{}), "installSeccomp casts one to the other")
}
