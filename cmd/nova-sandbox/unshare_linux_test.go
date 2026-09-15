//go:build linux

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// The wall Space cards run under: `nova-sandbox check` reports backend=unshare, and one
// wrapped command runs while the named secret (outside every list) does not. Both halves
// go through the REAL binary (toolBinary), not run() in-process, because the unshare body
// re-execs itself and the in-process path under `go test` re-enters TestMain instead.
//
// The whole sequence is the round-trip a friend runs through the changed verb: check, then
// wrap, and the second stands on the first's word.
func TestCheckReportsUnshareAndTheWallHolds(t *testing.T) {
	j := newJob(t)
	bin := toolBinary(t)

	check := exec.Command(bin, "check")
	checkOut, err := check.Output()
	if err != nil {
		t.Fatalf("check did not run: %v", err)
	}
	if !strings.Contains(string(checkOut), "backend=unshare") {
		t.Fatalf("check did not report backend=unshare: %s", checkOut)
	}

	work := exec.Command(bin, "--read", j.read, "--write", j.write, "--", "/bin/sh", "-c", "true")
	work.Env = j.env()
	if out, err := work.CombinedOutput(); err != nil {
		t.Fatalf("a wrapped command did not run inside the wall: %v\n%s", err, out)
	}

	deny := exec.Command(bin, "--read", j.read, "--write", j.write, "--", "/bin/sh", "-c", "cat '"+j.secret+"' > /dev/null")
	deny.Env = j.env()
	if err := deny.Run(); err == nil {
		t.Fatal("the secret was readable inside the wall")
	}
}

// --net-deny becomes a network namespace with no interface, and the OK line is the one
// grammar place that says so.
func TestNetDenyIsARealDenial(t *testing.T) {
	j := newJob(t)
	bin := toolBinary(t)
	cmd := exec.Command(bin, "--read", j.read, "--write", j.write, "--net-deny", "--", "/bin/sh", "-c", "true")
	cmd.Env = j.env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("a --net-deny wrap refused or died: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "net=denied") {
		t.Fatalf("the OK line does not say net=denied: %s", out)
	}
}
