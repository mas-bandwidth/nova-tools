package main

// --batches is the flag that carries bin/status.sh's last two lines into the verb: the
// swarm's first-attempt rate and the recurring-fault detector. It is a path and a flag,
// there is no default to guess, and a directory that cannot be read is refused rather than
// folded as a swarm with no faults. No test here starts nova-swarm or gh: the queue has no
// REPO file, so the status verb makes no subprocess call at all.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusBatchesFlagReachesTheSwarmLines(t *testing.T) {
	queue := t.TempDir()
	roots := t.TempDir()
	batches := t.TempDir()
	write(t, filepath.Join(batches, "batch-1.out"),
		"BATCH b1 n=6 done=1 abstain=5 in=0 out=0 usd=0 idle=0 stalled=0\n"+
			"card-a1 slot=1: ABSTAIN reason=no-branch log=9\n"+
			"card-a2 slot=2: ABSTAIN reason=no-branch log=9\n"+
			"card-a3 slot=3: ABSTAIN reason=no-branch log=9\n"+
			"card-a4 slot=4: ABSTAIN reason=no-branch log=9\n"+
			"card-a5 slot=5: ABSTAIN reason=no-branch log=9\n")

	exit, stdout, stderr := invokePulse(t, "status", "--queue", queue, "--roots", roots, "--batches", batches)
	if exit != 0 {
		t.Fatalf("status --batches exit = %d, want 0; stderr=%s", exit, stderr)
	}
	for _, want := range []string{
		"STATUS SWARM first_attempt=0.17 done=1 abstain=5 batches=1 hedge=opus-on-critical-path",
		"STATUS FAULT reason=no-branch count=5",
		"STATUS PIT-STOP reason=no-branch count=5 remedy=fix the machinery before more cards",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status does not carry %q:\n%s", want, stdout)
		}
	}
}

func TestStatusBatchesThatCannotBeReadIsRefused(t *testing.T) {
	exit, stdout, stderr := invokePulse(t, "status",
		"--queue", t.TempDir(), "--roots", t.TempDir(),
		"--batches", filepath.Join(t.TempDir(), "no-such-dir"))
	if exit != 2 {
		t.Fatalf("exit = %d, want 2; stdout=%s stderr=%s", exit, stdout, stderr)
	}
	if stdout != "" {
		t.Errorf("a refusal printed part of a report:\n%s", stdout)
	}
	if !strings.Contains(stderr, "--batches") {
		t.Errorf("the refusal does not name --batches:\n%s", stderr)
	}
	if got := strings.Count(strings.TrimSpace(stderr), "\n"); got != 0 {
		t.Errorf("the refusal is %d lines, want one remedy line:\n%s", got+1, stderr)
	}
}

// The help names the flag: a verb whose flag is only in the source is a flag nobody finds.
func TestHelpNamesBatches(t *testing.T) {
	exit, stdout, stderr := invokePulse(t, "help")
	if exit != 0 {
		t.Fatalf("help exit = %d; stderr=%s", exit, stderr)
	}
	for _, want := range []string{"--batches", "STATUS PIT-STOP", "DOWN"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the help does not name %q", want)
		}
	}
}
