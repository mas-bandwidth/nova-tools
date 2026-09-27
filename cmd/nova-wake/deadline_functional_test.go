//go:build functional

package main

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The real Git child must be killed when the whole-read deadline expires.
func TestAWedgedGitCannotHoldThePollOpen(t *testing.T) {
	busDir, anchor := newLaneBus(t)
	addLaneCommit(t, busDir, "from-peer", at.Add(-time.Minute),
		note{name: "a.md", from: peer, to: caller, subject: "the answer"})
	state := filepath.Join(t.TempDir(), "probe.state")
	seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)

	pidfile := filepath.Join(t.TempDir(), "wedged.pid")
	fakeGit(t)
	t.Setenv("NOVA_WAKE_FAKE_GIT_WEDGE", "--batch")
	t.Setenv("NOVA_WAKE_FAKE_GIT_PIDFILE", pidfile)

	// Assert completion and the child's exit, with a generous hang guard. The
	// product still uses the two-second interval as its whole-read deadline.
	done := make(chan result, 1)
	go func() {
		done <- probeAt(t, at.Add(time.Minute), "--bus", busDir, "--line", peer, "--state", state,
			"--as", caller, "--interval", "2s")
	}()
	var r result
	select {
	case r = <-done:
	case <-time.After(storeWait(t)):
		t.Fatal("the poll never completed after the whole-read deadline")
	}
	line := probeLine(t, r.stdout)
	if strings.Contains(line, "correlation=complete") {
		t.Errorf("a read that could not open an object is never complete:\n%s", line)
	}
	raw := read(t, pidfile)
	if raw == "" {
		t.Fatalf("the wedged git never ran; the shim is not on PATH\n%s", r.all())
	}
	pid, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		t.Fatalf("the pidfile holds %q", raw)
	}
	deadline := time.Now().Add(storeWait(t))
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(pid) {
		t.Errorf("pid %d is still running after the poll returned: a wedged git must be killed, not left behind", pid)
	}
}
