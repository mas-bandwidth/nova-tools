package main

// The seccomp-bpf program, built for one architecture from the wall's options. It is
// pure (no syscall), so it is built and interpreted in unit tests on every OS and for
// every architecture it supports. seccomp_linux.go installs it.
//
// Landlock does not path-check a unix-domain connect, so a command inside the wall could
// reach any same-uid socket: /proc/net/unix lists the paths. The only fence without a
// path check is to stop the command making a unix socket that can be pointed at a path.
// So by default socket(AF_UNIX) and every socketpair(AF_UNIX) except SOCK_STREAM are
// refused. A stream pair is connected to its twin: connect and sendto elsewhere fail
// with EISCONN, so it is allowed, and event loops (Python asyncio, Go, libuv) need one.
// A DGRAM pair can reconnect or sendto any datagram path, and a SEQPACKET pair was
// measured to sendto a datagram path too, so both are refused.
//
// Landlock fences TCP only. Under --net-deny the filter closes the rest by socket
// family: with no --net-allow only AF_UNIX (as above) and AF_NETLINK may be created, so
// there is no UDP, raw, packet or TCP socket at all; with --net-allow AF_INET and
// AF_INET6 are also allowed, but only as SOCK_STREAM with protocol 0 or IPPROTO_TCP,
// which is what Landlock checks. Every other family (SCTP, MPTCP, ICMP, SMC, TIPC,
// vsock, AF_ALG and the rest) is refused. ptrace and io_uring (whose queue runs
// operations seccomp does not see) are refused at any argument. The filter is inherited
// across fork and execve beside the Landlock ruleset.

const (
	// An action the kernel does not know is treated as a kill, so KILL_PROCESS is safe
	// on kernels older than 4.14 too.
	secRetKill       = 0x80000000 // SECCOMP_RET_KILL_PROCESS
	secRetErrnoEPERM = 0x00050000 | 1
	secRetAllow      = 0x7fff0000
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

// seccomp_data offsets; args are read by their low word (little-endian targets). The
// kernel reads socket's family, type and protocol as int, so the high word is ignored.
const (
	offNr   = 0
	offArch = 4
	offArg0 = 16
	offArg1 = 24
	offArg2 = 32
)

// Linux values, written out so the program is the same whatever OS builds it.
const (
	afUnix      = 1
	afInet      = 2
	afInet6     = 10
	afNetlink   = 16
	sockStream  = 1
	sockTypeMsk = 0xf // SOCK_TYPE_MASK: drops SOCK_NONBLOCK and SOCK_CLOEXEC
	ipprotoTCP  = 6
)

// archNums is one architecture's AUDIT_ARCH and the syscall numbers the filter names.
type archNums struct {
	audit                  uint32
	socket, socketpair     uint32
	ptrace                 uint32
	uringSetup, uringEnter uint32
	uringRegister          uint32
}

var archTable = map[string]archNums{
	"amd64": {audit: 0xc000003e, socket: 41, socketpair: 53, ptrace: 101, uringSetup: 425, uringEnter: 426, uringRegister: 427},
	"arm64": {audit: 0xc00000b7, socket: 198, socketpair: 199, ptrace: 117, uringSetup: 425, uringEnter: 426, uringRegister: 427},
}

// netRule is what the filter does to network sockets.
type netRule int

const (
	netOpen    netRule = iota // no --net-deny: nothing is promised, no rule
	netClosed                 // --net-deny: no inet or packet socket of any type
	netTCPOnly                // --net-deny --net-allow: inet SOCK_STREAM TCP only, for Landlock to check
)

// filterOpts is what the wall asks of the filter.
type filterOpts struct {
	fenceUnix bool // not --unix
	net       netRule
}

func filterOptsOf(w *wall) filterOpts {
	o := filterOpts{fenceUnix: !w.unix}
	switch {
	case w.netDeny && len(w.netPorts) > 0:
		o.net = netTCPOnly
	case w.netDeny:
		o.net = netClosed
	}
	return o
}

// insn is one instruction with symbolic jump targets; label names its own position.
// An empty target is the next instruction.
type insn struct {
	label  string
	code   uint16
	k      uint32
	jt, jf string
}

// sockFilter is struct sock_filter.
type sockFilter struct {
	Code uint16
	Jt   uint8
	Jf   uint8
	K    uint32
}

// assemble resolves the labels into the kernel's relative offsets. A missing label or
// a backward or too-long jump is a bug in buildFilter, not an input, so it panics.
func assemble(src []insn) []sockFilter {
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
		to, ok := at[label]
		if !ok || to <= i || to-i-1 > 255 {
			panic("seccomp: bad jump to " + label)
		}
		return uint8(to - i - 1)
	}
	out := make([]sockFilter, len(src))
	for i, in := range src {
		out[i] = sockFilter{Code: in.code, K: in.k, Jt: off(i, in.jt), Jf: off(i, in.jf)}
		if in.code == bpfJmpJA {
			out[i] = sockFilter{Code: in.code, K: uint32(off(i, in.jt))}
		}
	}
	return out
}

// buildFilter assembles the program for arch and names its rules in the order they
// apply, for show. Every rule the program holds is named here and nowhere else.
func buildFilter(arch archNums, o filterOpts) ([]sockFilter, []string) {
	p := []insn{
		{code: bpfLdWAbs, k: offArch},
		{code: bpfJeqK, k: arch.audit, jf: "kill"},
		{code: bpfLdWAbs, k: offNr},
		// x32 syscalls carry bit 30 and pass the x86_64 arch check: refuse them all,
		// or each would be an unfiltered twin of a refused syscall.
		{code: bpfJgeK, k: x32Bit, jt: "kill"},
	}
	names := []string{"kill: another architecture's syscall (i386, x32 compat)", "kill: an x32 syscall number"}
	sockets := o.fenceUnix || o.net != netOpen
	if sockets {
		p = append(p,
			insn{code: bpfJeqK, k: arch.socket, jt: "socket"},
			insn{code: bpfJeqK, k: arch.socketpair, jt: "pair"})
	}
	for _, nr := range []uint32{arch.ptrace, arch.uringSetup, arch.uringEnter, arch.uringRegister} {
		p = append(p, insn{code: bpfJeqK, k: nr, jt: "eperm"})
	}
	names = append(names, "eperm: ptrace", "eperm: io_uring_setup, io_uring_enter, io_uring_register")
	p = append(p, insn{code: bpfJmpJA, jt: "allow"})
	if sockets {
		unixTo := "allow"
		if o.fenceUnix {
			unixTo = "eperm"
			names = append(names, "eperm: socket(AF_UNIX)")
		}
		p = append(p,
			insn{label: "socket", code: bpfLdWAbs, k: offArg0},
			insn{code: bpfJeqK, k: afUnix, jt: unixTo})
		switch o.net {
		case netOpen:
			p = append(p, insn{code: bpfJmpJA, jt: "allow"})
		case netClosed:
			p = append(p,
				insn{code: bpfJeqK, k: afNetlink, jt: "allow"},
				insn{code: bpfJmpJA, jt: "eperm"})
			names = append(names, "eperm: socket of every family but AF_UNIX and AF_NETLINK (no TCP, UDP, raw or packet socket)")
		case netTCPOnly:
			p = append(p,
				insn{code: bpfJeqK, k: afNetlink, jt: "allow"},
				insn{code: bpfJeqK, k: afInet, jt: "inet"},
				insn{code: bpfJeqK, k: afInet6, jt: "inet", jf: "eperm"},
				insn{label: "inet", code: bpfLdWAbs, k: offArg1},
				insn{code: bpfAndK, k: sockTypeMsk},
				insn{code: bpfJeqK, k: sockStream, jf: "eperm"},
				insn{code: bpfLdWAbs, k: offArg2},
				insn{code: bpfJeqK, k: 0, jt: "allow"},
				insn{code: bpfJeqK, k: ipprotoTCP, jt: "allow", jf: "eperm"})
			names = append(names,
				"eperm: socket(AF_INET or AF_INET6) unless SOCK_STREAM with protocol 0 or IPPROTO_TCP (no UDP, raw, ICMP, SCTP or MPTCP)",
				"eperm: socket of every other family but AF_UNIX and AF_NETLINK (no packet socket)")
		}
		pairNotUnix := "allow"
		if o.net != netOpen {
			pairNotUnix = "eperm"
			names = append(names, "eperm: socketpair of any family but AF_UNIX")
		}
		p = append(p,
			insn{label: "pair", code: bpfLdWAbs, k: offArg0},
			insn{code: bpfJeqK, k: afUnix, jf: pairNotUnix})
		if o.fenceUnix {
			p = append(p,
				insn{code: bpfLdWAbs, k: offArg1},
				insn{code: bpfAndK, k: sockTypeMsk},
				insn{code: bpfJeqK, k: sockStream, jt: "allow", jf: "eperm"})
			names = append(names, "eperm: socketpair(AF_UNIX) unless SOCK_STREAM")
		} else {
			p = append(p, insn{code: bpfJmpJA, jt: "allow"})
		}
	}
	names = append(names, "allow: everything else")
	return assemble(append(p,
		insn{label: "allow", code: bpfRetK, k: secRetAllow},
		insn{label: "eperm", code: bpfRetK, k: secRetErrnoEPERM},
		insn{label: "kill", code: bpfRetK, k: secRetKill})), names
}
