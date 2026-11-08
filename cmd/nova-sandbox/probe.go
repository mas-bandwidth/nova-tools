package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// clientVerb is the hidden verb the probe runs inside the wall: nova-sandbox
// probe-client <kind> <arg>, exit 0 when the act succeeds and 1 when it fails. No shell
// or tool every host has can open a connection.
const clientVerb = "probe-client"

// dnsName is the probe's lookup: reserved by IANA, published, and in no stock
// /etc/hosts.
const dnsName = "example.com"

// probeStep is one walled run. arrived, when set, is what the far side saw: a datagram,
// a connection or a file. An allow needs the run and the arrival; a deny needs neither.
type probeStep struct {
	name, expect string
	flags, argv  []string
	arrived      func() bool
}

// probe proves the wall by running it, through this very binary, the way a caller does:
// a scratch directory holds an inside and an outside, and each act is tried walled
// without the flag under test (the control, which must succeed) and with it.
func probe(stdout io.Writer, env []string) int {
	self, err := os.Executable()
	root := ""
	if err == nil {
		root, err = os.MkdirTemp("", "nova-sandbox-probe-")
	}
	if err == nil {
		defer os.RemoveAll(root)
		root, err = filepath.EvalSymlinks(root)
	}
	in, out := filepath.Join(root, "in"), filepath.Join(root, "out")
	secret := filepath.Join(out, "secret")
	for _, d := range []string{in, out} {
		if err == nil {
			err = os.Mkdir(d, 0o700)
		}
	}
	if err == nil {
		err = os.WriteFile(secret, []byte("x"), 0o600)
	}
	if err != nil {
		fmt.Fprintf(stdout, "PROBE FAILED reason=%v\n", err)
		return 1
	}
	failed := 0
	line := func(name, expect, got, tail string) {
		fmt.Fprintf(stdout, "PROBE STEP name=%s expect=%s got=%s%s\n", name, expect, got, tail)
	}
	run := func(s probeStep) bool {
		cmd := exec.Command(self, slices.Concat([]string{"--write", in}, s.flags, []string{"--"}, s.argv)...)
		cmd.Env, cmd.Dir = env, in
		rc := 0
		if err := cmd.Run(); err != nil {
			rc = -1
			if ee, ok := err.(*exec.ExitError); ok {
				rc = ee.ExitCode()
			}
		}
		if rc == 125 && slices.Contains(s.flags, "--net-deny") {
			line(s.name, s.expect, "wall_refused", " note=--net-deny is not enforceable on this machine; the wall refuses it rather than pretend")
			return false
		}
		allowed := rc == 0
		if s.arrived != nil {
			if a := s.arrived(); s.expect == "allow" {
				allowed = allowed && a
			} else {
				allowed = allowed || a
			}
		}
		got, ok := "deny", !allowed
		if allowed {
			got, ok = "allow", s.expect == "allow"
		}
		tail := ""
		if !ok {
			tail, failed = " FAIL", failed+1
		}
		line(s.name, s.expect, got, tail)
		return allowed
	}
	client := func(kind, arg string) []string { return []string{self, clientVerb, kind, arg} }
	readSelf := []string{"--read", filepath.Dir(self)}
	deny, allowTo := []string{"--net-deny"}, func(addr string) []string { return []string{"--net-deny", "--net-allow", addr} }
	exists := func(p string) func() bool { return func() bool { _, err := os.Stat(p); return err == nil } }
	linux := backendName == "landlock"

	if os.WriteFile(filepath.Join(out, "control"), []byte("x"), 0o600) == nil {
		line("write_outside_control", "allow", "allow", "")
	} else {
		line("write_outside_control", "allow", "deny", " FAIL")
		failed++
	}
	run(probeStep{name: "write_inside", expect: "allow", argv: []string{"/bin/sh", "-c", "echo x > " + filepath.Join(in, "inside")}, arrived: exists(filepath.Join(in, "inside"))})
	run(probeStep{name: "write_outside", expect: "deny", argv: []string{"/bin/sh", "-c", "echo x > " + filepath.Join(out, "outside")}, arrived: exists(filepath.Join(out, "outside"))})
	run(probeStep{name: "read_outside", expect: "deny", argv: []string{"/bin/sh", "-c", "cat " + secret}})
	run(probeStep{name: "read_named", expect: "allow", flags: []string{"--read", out}, argv: []string{"/bin/sh", "-c", "cat " + secret}})

	// A unix socket outside the grants: Landlock does not path-check a unix connect, so
	// only the seccomp fence refuses it; dial reaches it by path, as an escape would.
	// --unix is Linux-only: the macOS profile denies a socket outside the writes anyway.
	sock := filepath.Join(out, "s")
	if ul, err := net.Listen("unix", sock); err != nil {
		note := " note=the probe could not listen on its own unix socket (" + err.Error() + "), so the step proves nothing"
		line("unix_deny", "deny", "untested", note)
		if linux {
			line("unix_allow", "allow", "untested", note)
		}
	} else {
		defer ul.Close()
		go acceptClose(ul, nil)
		run(probeStep{name: "unix_deny", expect: "deny", flags: readSelf, argv: client("dial", sock)})
		if linux {
			run(probeStep{name: "unix_allow", expect: "allow", flags: append(readSelf, "--unix"), argv: client("dial", sock)})
		}
	}

	// TCP: allowed is a listener --net-allow names; other is one it does not, whose
	// accepts the far-side judge counts.
	allowed, err1 := net.Listen("tcp", "127.0.0.1:0")
	other, err2 := net.Listen("tcp", "127.0.0.1:0")
	if err1 != nil || err2 != nil {
		fmt.Fprintf(stdout, "PROBE FAILED reason=tcp listeners: %v %v\n", err1, err2)
		return 1
	}
	defer allowed.Close()
	defer other.Close()
	go acceptClose(allowed, nil)
	accepted := make(chan struct{}, 16)
	go acceptClose(other, accepted)
	arrivedOn := func(ch chan struct{}) func() bool {
		return func() bool {
			select {
			case <-ch:
				return true
			case <-time.After(500 * time.Millisecond):
				return false
			}
		}
	}
	a, o := allowed.Addr().String(), other.Addr().String()
	run(probeStep{name: "net_control", expect: "allow", flags: readSelf, argv: client("dial", a)})
	run(probeStep{name: "net_deny", expect: "deny", flags: append(readSelf, deny...), argv: client("dial", a)})
	run(probeStep{name: "tcp_allow", expect: "allow", flags: append(readSelf, allowTo(a)...), argv: client("dial", a)})
	run(probeStep{name: "tcp_other_deny", expect: "deny", flags: append(readSelf, allowTo(a)...), argv: client("dial", o), arrived: arrivedOn(accepted)})

	// UDP: Landlock has no UDP rights, so on Linux these are the seccomp filter's. The
	// listener judges: a datagram that arrives got through, whatever the sender says.
	if u, err := net.ListenPacket("udp", "127.0.0.1:0"); err != nil {
		note := " note=the probe could not open its own UDP listener (" + err.Error() + "), so the step proves nothing"
		line("udp_control", "allow", "untested", note)
		line("udp_deny", "deny", "untested", note)
		line("udp_deny_allowmode", "deny", "untested", note)
	} else {
		defer u.Close()
		datagrams := make(chan struct{}, 16)
		go func() {
			buf := make([]byte, 64)
			for {
				if _, _, err := u.ReadFrom(buf); err != nil {
					return
				}
				datagrams <- struct{}{}
			}
		}()
		ua := u.LocalAddr().String()
		run(probeStep{name: "udp_control", expect: "allow", flags: readSelf, argv: client("udp", ua), arrived: arrivedOn(datagrams)})
		run(probeStep{name: "udp_deny", expect: "deny", flags: append(readSelf, deny...), argv: client("udp", ua), arrived: arrivedOn(datagrams)})
		run(probeStep{name: "udp_deny_allowmode", expect: "deny", flags: append(readSelf, allowTo(a)...), argv: client("udp", ua), arrived: arrivedOn(datagrams)})
	}

	// Linux only: TCP Fast Open connects inside sendto and listen() auto-binds, both
	// out of Landlock's sight, so --net-allow refuses them in seccomp. A host whose
	// kernel has client TFO off cannot show the deny, and says so.
	if linux {
		if run(probeStep{name: "tfo_control", expect: "allow", flags: readSelf, argv: client("tfo", o), arrived: arrivedOn(accepted)}) {
			run(probeStep{name: "tfo_deny", expect: "deny", flags: append(readSelf, allowTo(a)...), argv: client("tfo", o), arrived: arrivedOn(accepted)})
		} else {
			line("tfo_deny", "deny", "untested", " note=TCP Fast Open does not work here outside --net-allow, so the step proves nothing")
		}
		run(probeStep{name: "listen_control", expect: "allow", flags: readSelf, argv: client("listen", "tcp")})
		run(probeStep{name: "listen_deny", expect: "deny", flags: append(readSelf, allowTo(a)...), argv: client("listen", "tcp")})
	}

	// DNS: a name not in /etc/hosts. A host that cannot resolve it unwalled proves
	// nothing, and says so.
	if _, err := net.LookupHost(dnsName); err != nil {
		line("dns_deny", "deny", "untested", " note=this host cannot resolve "+dnsName+" outside the wall, so the step proves nothing here")
	} else {
		run(probeStep{name: "dns_control", expect: "allow", flags: readSelf, argv: client("resolve", dnsName)})
		run(probeStep{name: "dns_deny", expect: "deny", flags: append(readSelf, deny...), argv: client("resolve", dnsName)})
	}

	abi, _, _, _ := wallCheck()
	if failed > 0 {
		fmt.Fprintf(stdout, "PROBE FAILED backend=%s abi=%s failed=%d\n", backendName, abi, failed)
		return 1
	}
	fmt.Fprintf(stdout, "PROBE OK backend=%s abi=%s\n", backendName, abi)
	return 0
}

// acceptClose accepts and closes connections until the listener closes, telling seen
// (when set) of each.
func acceptClose(l net.Listener, seen chan struct{}) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		c.Close()
		if seen != nil {
			seen <- struct{}{}
		}
	}
}

// probeClient is the act inside the wall, by kind:
//
//	dial <addr>     a connect: an absolute path is a unix socket, anything else TCP
//	udp <addr>      one datagram sent to a UDP address
//	resolve <name>  a lookup through the system resolver
//	tfo <addr>      Linux: a TCP Fast Open send (MSG_FASTOPEN), which connects
//	listen tcp      Linux: listen() on a TCP socket that was never bound
func probeClient(args []string) int {
	if len(args) != 2 {
		return 2
	}
	kind, arg := args[0], args[1]
	var err error
	switch kind {
	case "dial", "udp":
		network := "tcp"
		if kind == "udp" {
			network = "udp"
		} else if strings.HasPrefix(arg, "/") {
			network = "unix"
		}
		var c net.Conn
		if c, err = net.DialTimeout(network, arg, 3*time.Second); err == nil {
			if kind == "udp" {
				_, err = c.Write([]byte("probe"))
			}
			c.Close()
		}
	case "resolve":
		_, err = net.LookupHost(arg)
	case "tfo", "listen":
		err = rawClient(kind, arg)
	default:
		return 2
	}
	if err != nil {
		return 1
	}
	return 0
}
