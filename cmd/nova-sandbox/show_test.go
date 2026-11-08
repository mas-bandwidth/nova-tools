package main

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanLinuxRefusals(t *testing.T) {
	t.Parallel()
	full := linuxHost{landlock: true, abi: 6, seccomp: true}
	reasonsOf := func(w *wall, h linuxHost) []string {
		_, bad := planLinux(w, h)
		return reasons(bad)
	}
	assert.Empty(t, reasonsOf(&wall{netDeny: true}, full))
	assert.Equal(t, []string{"no_sandbox"}, reasonsOf(&wall{}, linuxHost{}))
	assert.Equal(t, []string{"net_unenforceable"}, reasonsOf(&wall{netDeny: true}, linuxHost{landlock: true, abi: 3, seccomp: true}))
	_, bad := planLinux(&wall{netDeny: true, unix: true}, linuxHost{landlock: true, abi: 6})
	require.Len(t, bad, 1)
	assert.Contains(t, bad[0].text, "net-deny needs seccomp on this kernel", "never a --net-deny that leaves UDP open")
	assert.Equal(t, []string{"no_seccomp"}, reasonsOf(&wall{}, linuxHost{landlock: true, abi: 6}))
	p, bad := planLinux(&wall{unix: true}, linuxHost{landlock: true, abi: 9})
	require.Empty(t, bad, "--unix without --net-deny runs on Landlock alone")
	assert.False(t, p.seccomp)
	assert.Equal(t, maxABI, p.abi, "a newer kernel gets the ruleset of the newest ABI this tool knows")
}

const wantShowLinux = `wall backend=landlock abi=4 seccomp=on
fs handled=execute,write_file,read_file,read_dir,remove_and_make,refer,truncate
path access=read path=/usr from=root
path access=read path=/bin from=root
path access=read path=/sbin from=root
path access=read path=/lib from=root
path access=read path=/lib64 from=root state=absent
path access=read path=/etc from=root
path access=read path=/run/systemd/resolve from=root
path access=read path=/opt from=root
path access=read path=/dev from=root
path access=read path=/proc from=root
path access=write path=/dev/null from=root
path access=write path=/dev/tty from=root
path access=write path=/dev/shm from=root
path access=read path=/x from=--read
path access=write path="/y z" from=--write
net rule=closed tcp=deny by=landlock,seccomp udp=deny raw=deny packet=deny by=seccomp dns=/etc/hosts_only
seccomp action=kill rule="another architecture's syscall (i386, x32 compat)"
seccomp action=kill rule="an x32 syscall number"
seccomp action=eperm rule=ptrace
seccomp action=eperm rule="io_uring_setup, io_uring_enter, io_uring_register"
seccomp action=eperm rule=socket(AF_UNIX)
seccomp action=eperm rule="socket of every family but AF_UNIX and AF_NETLINK (no TCP, UDP, raw or packet socket)"
seccomp action=eperm rule="socket(AF_NETLINK) unless NETLINK_ROUTE (no sock_diag, which lists other processes' connections)"
seccomp action=eperm rule="socketpair of any family but AF_UNIX"
seccomp action=eperm rule="socketpair(AF_UNIX) unless SOCK_STREAM"
seccomp action=allow rule="everything else"
scope none note=below landlock abi 6 a signal can reach a process outside the wall
fds close=above_stderr
command exec=/bin/true argv=["true","-x"]
`

func TestFormatLinux(t *testing.T) {
	t.Parallel()
	if _, ok := archTable[runtime.GOARCH]; !ok {
		t.Skip("no seccomp program for this architecture")
	}
	w := &wall{reads: []string{"/x"}, writes: []string{"/y z"}, netDeny: true, command: "/bin/true", argv: []string{"true", "-x"}}
	p, bad := planLinux(w, linuxHost{landlock: true, abi: 4, seccomp: true})
	require.Empty(t, bad)
	got := formatLinux(w, p, func(path string) bool { return path != "/lib64" })
	assert.Equal(t, wantShowLinux, got)

	w = &wall{netDeny: true, netPorts: []uint16{8080, 9}, unix: true, keepFDs: true}
	p, _ = planLinux(w, linuxHost{landlock: true, abi: 6, seccomp: true})
	got = formatLinux(w, p, func(string) bool { return true })
	assert.Contains(t, got, "net rule=tcp_allow tcp_connect=allow ports=8080,9 tcp_bind=deny by=landlock listen=deny accept=deny fastopen=deny udp=deny raw=deny packet=deny by=seccomp")
	assert.Contains(t, got, "seccomp action=eperm rule=\"listen, accept, accept4 on every socket")
	assert.Contains(t, got, "scope signal=wall abstract_unix=wall\n")
	assert.Contains(t, got, "fds keep=all")
	assert.NotContains(t, got, "rule=socket(AF_UNIX)\n", "--unix")
	assert.NotContains(t, got, "command ", "no command given")

	w = &wall{unix: true}
	p, _ = planLinux(w, linuxHost{landlock: true, abi: 6})
	got = formatLinux(w, p, func(string) bool { return true })
	assert.Contains(t, got, "wall backend=landlock abi=6 seccomp=off\n")
	assert.Contains(t, got, "seccomp action=none")
	assert.Contains(t, got, "net rule=open")
}

func TestFormatDarwin(t *testing.T) {
	t.Parallel()
	w := darwinWall()
	w.netDeny, w.netPorts = true, []uint16{8080}
	text, params, err := darwinProfile(w)
	require.NoError(t, err)
	got := formatDarwin(w, "/usr/bin/sandbox-exec", text, params)
	head, profile, ok := strings.Cut(got, "profile\n")
	require.True(t, ok)
	assert.Equal(t, text, profile, "the profile is printed verbatim, last")
	assert.Equal(t, `wall backend=sandbox-exec bin=/usr/bin/sandbox-exec
param name=READ0 path=/r/one
param name=WRITE0 path=/w/job
param name=WRITE1 path=/w/data
net rule=localhost_allow ports=8080 note=outbound to localhost on those ports, TCP and UDP; no other IP and no DNS socket
fds close=above_stderr
`, head)
}
