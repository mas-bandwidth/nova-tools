package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// data is the seccomp_data the interpreter evaluates against.
type data struct{ arch, nr, arg0, arg1, arg2 uint32 }

// verdict interprets the classic-BPF program the way the kernel does, for the opcodes
// the program uses, and returns its return value. A jump out of range or an unknown
// opcode fails the test: the kernel would reject that program.
func verdict(t *testing.T, p []sockFilter, d data) uint32 {
	t.Helper()
	field := map[uint32]uint32{offArch: d.arch, offNr: d.nr, offArg0: d.arg0, offArg1: d.arg1, offArg2: d.arg2}
	var a uint32
	for pc := 0; pc < len(p); {
		in := p[pc]
		next := pc + 1
		switch in.Code {
		case bpfLdWAbs:
			v, ok := field[in.K]
			require.True(t, ok, "load of an unknown offset %d at %d", in.K, pc)
			a = v
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

// Linux socket constants the cases use beyond bpf.go's.
const (
	sockDgram     = 2
	sockRaw       = 3
	sockSeqpacket = 5
	sockNonblock  = 0x800
	sockCloexec   = 0x80000
	afPacket      = 17
	afAlg         = 38
	afVsock       = 40
	afSMC         = 43
	ipprotoICMP   = 1
	ipprotoUDP    = 17
	ipprotoSCTP   = 132
	ipprotoMPTCP  = 262
	sysGetpid     = 39 // amd64; on arm64 172. Only "not a filtered number" matters.
)

var (
	open      = filterOpts{}
	strict    = filterOpts{fenceUnix: true}
	closed    = filterOpts{fenceUnix: true, net: netClosed}
	closedU   = filterOpts{net: netClosed} // --unix --net-deny
	tcpOnly   = filterOpts{fenceUnix: true, net: netTCPOnly}
	tcpOnlyU  = filterOpts{net: netTCPOnly}
	allOpts   = []filterOpts{open, strict, closed, closedU, tcpOnly, tcpOnlyU}
	optsNames = []string{"--unix", "default", "--net-deny", "--unix --net-deny", "--net-allow", "--unix --net-allow"}
)

const (
	A = secRetAllow
	E = secRetErrnoEPERM
	K = secRetKill
)

// TestSeccompFilter runs every rule through the interpreter, for each architecture's
// numbers and each set of flags. The want column is, in allOpts order: --unix,
// default, --net-deny, --unix --net-deny, --net-allow, --unix --net-allow.
func TestSeccompFilter(t *testing.T) {
	t.Parallel()
	for goarch, arch := range archTable {
		progs := make([][]sockFilter, len(allOpts))
		for i, o := range allOpts {
			progs[i], _ = buildFilter(arch, o)
		}
		sock := func(fam, typ, proto uint32) data {
			return data{arch: arch.audit, nr: arch.socket, arg0: fam, arg1: typ, arg2: proto}
		}
		pair := func(fam, typ uint32) data { return data{arch: arch.audit, nr: arch.socketpair, arg0: fam, arg1: typ} }
		for _, c := range []struct {
			name string
			d    data
			want [6]uint32
		}{
			{"wrong arch", data{arch: arch.audit ^ 1, nr: sysGetpid}, [6]uint32{K, K, K, K, K, K}},
			{"x32 nr", data{arch: arch.audit, nr: x32Bit | arch.socket, arg0: afInet}, [6]uint32{K, K, K, K, K, K}},
			{"getpid", data{arch: arch.audit, nr: sysGetpid}, [6]uint32{A, A, A, A, A, A}},
			{"ptrace", data{arch: arch.audit, nr: arch.ptrace}, [6]uint32{E, E, E, E, E, E}},
			{"io_uring_setup", data{arch: arch.audit, nr: arch.uringSetup}, [6]uint32{E, E, E, E, E, E}},
			{"io_uring_enter", data{arch: arch.audit, nr: arch.uringEnter}, [6]uint32{E, E, E, E, E, E}},
			{"io_uring_register", data{arch: arch.audit, nr: arch.uringRegister}, [6]uint32{E, E, E, E, E, E}},

			{"socket unix stream", sock(afUnix, sockStream, 0), [6]uint32{A, E, E, A, E, A}},
			{"socket unix dgram", sock(afUnix, sockDgram, 0), [6]uint32{A, E, E, A, E, A}},
			{"socket netlink", sock(afNetlink, sockRaw|sockCloexec, 0), [6]uint32{A, A, A, A, A, A}},

			{"socket inet tcp", sock(afInet, sockStream, 0), [6]uint32{A, A, E, E, A, A}},
			{"socket inet tcp explicit", sock(afInet, sockStream|sockNonblock|sockCloexec, ipprotoTCP), [6]uint32{A, A, E, E, A, A}},
			{"socket inet6 tcp", sock(afInet6, sockStream|sockCloexec, 0), [6]uint32{A, A, E, E, A, A}},
			{"socket inet udp", sock(afInet, sockDgram, 0), [6]uint32{A, A, E, E, E, E}},
			{"socket inet6 udp", sock(afInet6, sockDgram|sockNonblock, ipprotoUDP), [6]uint32{A, A, E, E, E, E}},
			{"socket inet icmp ping", sock(afInet, sockDgram, ipprotoICMP), [6]uint32{A, A, E, E, E, E}},
			{"socket inet raw", sock(afInet, sockRaw, ipprotoUDP), [6]uint32{A, A, E, E, E, E}},
			{"socket inet sctp stream", sock(afInet, sockStream, ipprotoSCTP), [6]uint32{A, A, E, E, E, E}},
			{"socket inet sctp seqpacket", sock(afInet6, sockSeqpacket, ipprotoSCTP), [6]uint32{A, A, E, E, E, E}},
			{"socket inet mptcp", sock(afInet, sockStream, ipprotoMPTCP), [6]uint32{A, A, E, E, E, E}},
			{"socket packet", sock(afPacket, sockRaw, 0x0300), [6]uint32{A, A, E, E, E, E}},
			{"socket smc", sock(afSMC, sockStream, 0), [6]uint32{A, A, E, E, E, E}},
			{"socket vsock", sock(afVsock, sockStream, 0), [6]uint32{A, A, E, E, E, E}},
			{"socket alg", sock(afAlg, 5, 0), [6]uint32{A, A, E, E, E, E}},

			{"pair unix stream", pair(afUnix, sockStream|sockCloexec), [6]uint32{A, A, A, A, A, A}},
			{"pair unix seqpacket", pair(afUnix, sockSeqpacket|sockNonblock), [6]uint32{A, E, E, A, E, A}},
			{"pair unix dgram", pair(afUnix, sockDgram), [6]uint32{A, E, E, A, E, A}},
			{"pair inet", pair(afInet, sockStream), [6]uint32{A, A, E, E, E, E}},
		} {
			for i := range allOpts {
				assert.Equal(t, c.want[i], verdict(t, progs[i], c.d), "%s %s under %s", goarch, c.name, optsNames[i])
			}
		}
	}
}

// TestFilterNames: every rule the program holds is named, so show lists what applies.
func TestFilterNames(t *testing.T) {
	t.Parallel()
	_, open := buildFilter(archTable["amd64"], filterOpts{})
	assert.Equal(t, []string{"kill: another architecture's syscall (i386, x32 compat)", "kill: an x32 syscall number",
		"eperm: ptrace", "eperm: io_uring_setup, io_uring_enter, io_uring_register", "allow: everything else"}, open)
	_, closed := buildFilter(archTable["amd64"], filterOpts{fenceUnix: true, net: netClosed})
	assert.Contains(t, closed, "eperm: socket(AF_UNIX)")
	assert.Contains(t, closed, "eperm: socket of every family but AF_UNIX and AF_NETLINK (no TCP, UDP, raw or packet socket)")
	assert.Contains(t, closed, "eperm: socketpair of any family but AF_UNIX")
	assert.Contains(t, closed, "eperm: socketpair(AF_UNIX) unless SOCK_STREAM")
	_, tcp := buildFilter(archTable["arm64"], filterOpts{net: netTCPOnly})
	assert.NotContains(t, tcp, "eperm: socket(AF_UNIX)", "--unix leaves AF_UNIX open")
	assert.Contains(t, tcp, "eperm: socket(AF_INET or AF_INET6) unless SOCK_STREAM with protocol 0 or IPPROTO_TCP (no UDP, raw, ICMP, SCTP or MPTCP)")
}

func TestFilterOptsOf(t *testing.T) {
	t.Parallel()
	assert.Equal(t, filterOpts{fenceUnix: true}, filterOptsOf(&wall{}))
	assert.Equal(t, filterOpts{net: netClosed}, filterOptsOf(&wall{unix: true, netDeny: true}))
	assert.Equal(t, filterOpts{fenceUnix: true, net: netTCPOnly}, filterOptsOf(&wall{netDeny: true, netPorts: []uint16{8080}}))
}

func TestAssembleRefusesABadJump(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { assemble([]insn{{code: bpfJmpJA, jt: "nowhere"}}) })
	assert.Panics(t, func() { assemble([]insn{{label: "back", code: bpfRetK}, {code: bpfJmpJA, jt: "back"}}) })
}
