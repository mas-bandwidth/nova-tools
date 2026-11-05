package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// probe proves the wall by running it, through this very binary, the way a caller does:
// a scratch directory holds an inside and an outside, and the same writes are tried
// unwalled (the control, which must succeed) and walled.
func probe(stdout io.Writer, env []string) int {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stdout, "PROBE FAILED reason=%v\n", err)
		return 1
	}
	root, err := os.MkdirTemp("", "nova-sandbox-probe-")
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		fmt.Fprintf(stdout, "PROBE FAILED reason=%v\n", err)
		return 1
	}
	defer os.RemoveAll(root)
	in, out := filepath.Join(root, "in"), filepath.Join(root, "out")
	for _, d := range []string{in, out} {
		if err := os.Mkdir(d, 0o700); err != nil {
			fmt.Fprintf(stdout, "PROBE FAILED reason=%v\n", err)
			return 1
		}
	}
	secret := filepath.Join(out, "secret")
	if err := os.WriteFile(secret, []byte("x"), 0o600); err != nil {
		fmt.Fprintf(stdout, "PROBE FAILED reason=%v\n", err)
		return 1
	}
	// walled runs argv inside a wall whose only grant is a write to in, plus extra flags.
	walled := func(extra []string, argv ...string) int {
		args := append([]string{"--write", in}, extra...)
		cmd := exec.Command(self, append(append(args, "--"), argv...)...)
		cmd.Env, cmd.Dir = env, in
		if err := cmd.Run(); err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				return ee.ExitCode()
			}
			return -1
		}
		return 0
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	failed := 0
	step := func(name, expect string, ok bool, got string) {
		verdict := ""
		if !ok {
			verdict, failed = " FAIL", failed+1
		}
		fmt.Fprintf(stdout, "PROBE STEP name=%s expect=%s got=%s%s\n", name, expect, got, verdict)
	}
	gotOf := func(allowed bool) string {
		if allowed {
			return "allow"
		}
		return "deny"
	}

	control := filepath.Join(out, "control")
	step("write_outside_control", "allow", os.WriteFile(control, []byte("x"), 0o600) == nil, "allow")

	inside := filepath.Join(in, "inside")
	rc := walled(nil, "/bin/sh", "-c", "echo x > "+inside)
	step("write_inside", "allow", rc == 0 && exists(inside), gotOf(rc == 0 && exists(inside)))

	outside := filepath.Join(out, "outside")
	rc = walled(nil, "/bin/sh", "-c", "echo x > "+outside)
	step("write_outside", "deny", rc != 0 && !exists(outside), gotOf(rc == 0 || exists(outside)))

	rc = walled(nil, "/bin/sh", "-c", "cat "+secret)
	step("read_outside", "deny", rc != 0, gotOf(rc == 0))

	rc = walled([]string{"--read", out}, "/bin/sh", "-c", "cat "+secret)
	step("read_named", "allow", rc == 0, gotOf(rc == 0))

	// --net-deny: a listener this process owns must be unreachable from the wall, and
	// reachable without it.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		step("net_control", "allow", false, "listen_failed")
	} else {
		defer l.Close()
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				c.Close()
			}
		}()
		addr := l.Addr().String()
		// the in-wall client is this binary, so its directory is read wherever it is installed
		readSelf := []string{"--read", filepath.Dir(self)}
		step("net_control", "allow", walled(readSelf, self, "dial", addr) == 0, "allow")
		switch rc := walled(append(readSelf, "--net-deny"), self, "dial", addr); rc {
		case 125:
			fmt.Fprintf(stdout, "PROBE STEP name=net_deny expect=deny got=wall_refused note=--net-deny is not enforceable on this machine; the wall refuses it rather than pretend\n")
		default:
			step("net_deny", "deny", rc != 0, gotOf(rc == 0))
		}
	}
	abi, _, _, _ := wallCheck()
	if failed > 0 {
		fmt.Fprintf(stdout, "PROBE FAILED backend=%s abi=%s failed=%d\n", backendName, abi, failed)
		return 1
	}
	fmt.Fprintf(stdout, "PROBE OK backend=%s abi=%s\n", backendName, abi)
	return 0
}

// dial exits 0 when a TCP connect to addr succeeds. It is the probe's client inside the
// wall: no shell or tool every host has can open a TCP connection.
func dial(args []string) int {
	if len(args) != 1 {
		return 2
	}
	c, err := net.DialTimeout("tcp", args[0], 3*time.Second)
	if err != nil {
		return 1
	}
	c.Close()
	return 0
}
