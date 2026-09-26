//go:build slow

// The tests of this package that cost more than the per-commit run can pay:
// over five seconds each on the Linux bench, or a deadline, wedge or wall-clock
// bound proved by waiting it out. They are behind the `slow` build tag, so
// go-test-cmd and go-test-internal do not build them, and
// .github/workflows/nightly-slow.yml (and `make test-slow`) runs them whole,
// every night. Each carries the measurement that moved it. Nothing here is
// skipped or weakened.

package main

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// SLOW: 2.1 s on hetzner at dev 64b9bec48, a deadline/wedge/wall bound proved by waiting it out.
func TestAWedgedGitCannotHoldThePollOpen(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: wedges git and waits a real deadline for it to be killed; runs on the self-hosted legs and nightly")
	}
	busDir, anchor := newLaneBus(t)
	addLaneCommit(t, busDir, "from-peer", at.Add(-time.Minute),
		note{name: "a.md", from: peer, to: caller, subject: "the answer"})
	state := filepath.Join(t.TempDir(), "probe.state")
	seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)

	pidfile := filepath.Join(t.TempDir(), "wedged.pid")
	fakeGit(t)
	t.Setenv("NOVA_WAKE_FAKE_GIT_WEDGE", "--batch")
	t.Setenv("NOVA_WAKE_FAKE_GIT_PIDFILE", pidfile)

	started := time.Now()
	r := probeAt(t, at.Add(time.Minute), "--bus", busDir, "--line", peer, "--state", state,
		"--as", caller, "--interval", "2s")
	elapsed := time.Since(started)
	// The number is loose on purpose: what this test proves is that the poll
	// ENDS, and against the code this repairs it did not -- the package timed
	// out at ten minutes with the child still sleeping. The read's own bound is
	// asserted tightly in internal/wake's TestAWedgedProcessIsKilledByTheWhole
	// ReadBound; here the clock also carries the probe's own git calls, each of
	// them through the shim and a real git under it.
	if elapsed > 20*time.Second {
		t.Errorf("the poll took %s; the whole read is bounded by --interval or 30s, whichever is smaller", elapsed)
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
	deadline := time.Now().Add(3 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(pid) {
		t.Errorf("pid %d is still running after the poll returned: a wedged git must be killed, not left behind", pid)
	}
}
