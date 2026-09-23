package main

// TestIssue2381 reproduces nova-tools#2381 as its title states it: nova-pulse fill -- a
// launcher refusal (capacity, disk, unreachable; rc 2 before any process) is HANDED BACK
// to the queue, never raised as an UNKNOWN start.
//
// The break, on the 2026-09-20 load test: 191 cards were raised UNKNOWN in twenty-eight
// minutes, every one a launcher line like `LAUNCH REFUSED space card-...: CAPACITY
// cores=32 load=66 free=193G memfree=78G allowed=-22` followed by `LAUNCHER EXIT rc=2`.
// Nothing had started; the card was simply dropped and had to be requeued by hand.
//
// A refusal is slow on exactly the bench that causes it: the launcher learns "over
// capacity" or "unreachable" over an ssh that the overloaded bench answers late, so the
// rc 2 lands AFTER the launch grace. A fill that takes "still running at the grace" as
// "launched" counts the card launched with no job behind it -- the drop the loop then
// raised UNKNOWN. The fill owes the launcher's contract one refusal window: a child that
// exits inside it reports its code exactly as an in-grace exit (rc 2 is handed back);
// only a child still running when the window closes has launched.

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIssue2381(t *testing.T) {
	specs := fakePATH(t)
	dir := t.TempDir()
	// The launcher is a bench at capacity: it answers its own ssh past the launch
	// grace, then refuses before any process -- rc 2, the contract's code.
	fakeTool(t, specs, "nova-swarm", fakeSpec{Default: fakeRule{
		SleepMS: 1400,
		Stderr:  "LAUNCH REFUSED bench-a card-001: CAPACITY cores=32 load=66 free=193G memfree=78G allowed=-22",
		Exit:    2,
	}})
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeMainFile(t, ready, "card-001.md", "a card\n")

	var out, errb bytes.Buffer
	_ = run([]string{"fill",
		"--ready", ready, "--launched", launched,
		"--machines", fillMachines(t, dir, "bench-a"),
		"--bench", "bench-a", "--capacity", "1", "--once",
		"--launch-grace", "1s",
		"--launcher", filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix()),
	}, &out, &errb, time.Now().UTC())

	// HANDED BACK: the card is the queue's again, in ready, not dropped into launched
	// with nothing started behind it.
	if got := fillCount(t, ready); got != 1 {
		t.Errorf("a refused card is handed back to ready: ready holds %d cards, want 1 (stdout=%q stderr=%q)", got, out.String(), errb.String())
	}
	if got := fillCount(t, launched); got != 0 {
		t.Errorf("a refused card never counted as a start: launched holds %d cards, want 0 (stdout=%q stderr=%q)", got, out.String(), errb.String())
	}
	if strings.Contains(out.String(), "launched=1") {
		t.Errorf("the FILL line counted a refused card as launched: %q", out.String())
	}
	// A one-line reason in the loop log: the refusal names the card and carries the
	// launcher's own reason, so the next hand reads why.
	if !strings.Contains(errb.String(), "card-001.md") || !strings.Contains(errb.String(), "LAUNCH REFUSED") {
		t.Errorf("the loop log does not carry the refusal's one-line reason: %q", errb.String())
	}
}
