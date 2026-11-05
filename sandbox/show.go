package main

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

// show's text. Both formatters are pure, so they are unit-tested on every OS; the
// wall_*.go describeWall feeds them what this host offers.

// field renders a path or word as one whitespace-free value: quoted when it holds a
// space, a quote, an = or a control character.
func field(s string) string {
	if s == "" || strings.ContainsFunc(s, func(r rune) bool { return r <= ' ' || r == '"' || r == '=' || r == 0x7f }) {
		return strconv.Quote(s)
	}
	return s
}

func argvField(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = strconv.Quote(a)
	}
	return "[" + strings.Join(q, ",") + "]"
}

// fsNames names the handled filesystem rights.
func fsNames(fs uint64) string {
	var out []string
	for _, n := range []struct {
		bit  uint64
		name string
	}{{fsExecute, "execute"}, {fsWriteFile, "write_file"}, {fsReadFile, "read_file"}, {fsReadDir, "read_dir"},
		{fsMakeAll, "remove_and_make"}, {fsRefer, "refer"}, {fsTruncate, "truncate"}, {fsIoctlDev, "ioctl_dev"}} {
		if fs&n.bit == n.bit {
			out = append(out, n.name)
		}
	}
	return strings.Join(out, ",")
}

func commandLines(b *strings.Builder, w *wall) {
	if w.keepFDs {
		b.WriteString("fds keep=all note=--keep-fds: inherited descriptors above stderr stay usable\n")
	} else {
		b.WriteString("fds close=above_stderr\n")
	}
	if w.command != "" {
		fmt.Fprintf(b, "command exec=%s argv=%s\n", field(w.command), argvField(w.argv))
	}
}

// formatLinux is the Linux plan, one line per rule, in the order execWall applies them.
func formatLinux(w *wall, p *linuxPlan, exists func(string) bool) string {
	var b strings.Builder
	sec := "on"
	if !p.seccomp {
		sec = "off"
	}
	fmt.Fprintf(&b, "wall backend=landlock abi=%d seccomp=%s\n", p.abi, sec)
	fmt.Fprintf(&b, "fs handled=%s\n", fsNames(p.rs.fs))
	for _, ru := range p.rs.rules {
		access, from := "write", "root"
		if ru.mask == readMask {
			access = "read"
		}
		if ru.must {
			from = "--" + access
		}
		state := ""
		if !ru.must && !exists(ru.path) {
			state = " state=absent"
		}
		fmt.Fprintf(&b, "path access=%s path=%s from=%s%s\n", access, field(ru.path), from, state)
	}
	switch filterOptsOf(w).net {
	case netOpen:
		b.WriteString("net rule=open note=no --net-deny: nothing is promised; TCP, UDP and raw sockets are open\n")
	case netClosed:
		b.WriteString("net rule=closed tcp=deny by=landlock,seccomp udp=deny raw=deny packet=deny by=seccomp dns=/etc/hosts_only\n")
	case netTCPOnly:
		fmt.Fprintf(&b, "net rule=tcp_allow tcp_connect=allow ports=%s tcp_bind=deny by=landlock listen=deny accept=deny fastopen=deny udp=deny raw=deny packet=deny by=seccomp dns=/etc/hosts_or_a_resolver_on_an_allowed_tcp_port note=landlock filters by port: an allowed port is open on every address\n", portList(w.netPorts))
	}
	if p.seccomp {
		arch := archTable[runtime.GOARCH]
		_, names := buildFilter(arch, p.filter)
		for _, n := range names {
			action, rule, _ := strings.Cut(n, ": ")
			fmt.Fprintf(&b, "seccomp action=%s rule=%s\n", action, field(rule))
		}
	} else {
		b.WriteString("seccomp action=none note=no filter mode in this kernel: unix sockets, ptrace and io_uring are open (--unix accepted that)\n")
	}
	if p.rs.scope != 0 {
		b.WriteString("scope signal=wall abstract_unix=wall\n")
	} else {
		b.WriteString("scope none note=below landlock abi 6 a signal can reach a process outside the wall\n")
	}
	commandLines(&b, w)
	return b.String()
}

// formatDarwin is the macOS plan: the parameters, the net rule and the profile verbatim.
func formatDarwin(w *wall, bin, text string, params []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "wall backend=sandbox-exec bin=%s\n", field(bin))
	for _, kv := range params {
		k, v, _ := strings.Cut(kv, "=")
		fmt.Fprintf(&b, "param name=%s path=%s\n", k, field(v))
	}
	switch filterOptsOf(w).net {
	case netOpen:
		b.WriteString("net rule=open note=no --net-deny: outbound IP and the DNS socket are open; inbound and bind are denied\n")
	case netClosed:
		b.WriteString("net rule=closed note=no outbound IP of any protocol and no DNS socket; inbound and bind are denied\n")
	case netTCPOnly:
		fmt.Fprintf(&b, "net rule=localhost_allow ports=%s note=outbound to localhost on those ports, TCP and UDP; no other IP and no DNS socket\n", portList(w.netPorts))
	}
	commandLines(&b, w)
	b.WriteString("profile\n")
	b.WriteString(text)
	return b.String()
}
