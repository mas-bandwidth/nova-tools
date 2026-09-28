package main

// nova-tools #179 — Coordination: rapidly reprioritize, correct and stop
// already-distributed work.
//
// A human or coordinator must be able to interrupt the plan mid-stream:
// prioritize a release, correct an already-issued task, or stop work quickly.
// On base there is NO control event a coordinator can append: the only verb
// that carries a reason is close, and a close reads as work DONE, so "stop
// this" and "this is finished" fold into the same line and a new chat message
// alone proves nothing about what a running descendant saw. The control verb
// records the interruption as itself — its kind (priority, correct, pause,
// cancel, stop), its reason, the revision the controller saw (after=) and a
// stable event id — so the log distinguishes interrupted work from done work,
// and the board's own fences apply: a live take refuses without --anyway, and
// a closed card has nothing running left to interrupt.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue179(t *testing.T) {
	t.Parallel()
	b := newBench(t)

	// ------------------------------------------------------ stop distributed work
	// A card is out with a worker: distributed work a coordinator must be able
	// to interrupt mid-stream.
	distributed := b.add(plain("rowan", "migrate the release board to the new schema")...)
	exit, _, stderr := b.run(b.board("take", "--as", "glenn", "--card", distributed, "--stale", "10m")...)
	if exit != 0 {
		t.Fatalf("take exit %d: %s", exit, stderr)
	}

	// A stop that does not say it goes over the worker's live take is refused,
	// and nothing is written: the interruption must be authorized out loud.
	exit, _, stderr = b.run(b.board("control", "--as", "rowan", "--card", distributed, "--stale", "10m",
		"--kind", "stop", "--reason", "the release is cut; stop the migration")...)
	if exit != 1 {
		t.Fatalf("control over a live take without --anyway: exit %d, want 1: %s", exit, stderr)
	}
	if !strings.Contains(stderr, "--anyway") {
		t.Errorf("the refusal must name --anyway, got: %s", stderr)
	}

	// The authorized stop lands in one bounded append and says what it did.
	exit, stdout, stderr := b.run(b.board("control", "--as", "rowan", "--card", distributed, "--stale", "10m",
		"--kind", "stop", "--reason", "the release is cut; stop the migration", "--anyway")...)
	if exit != 0 {
		t.Fatalf("control stop exit %d: %s", exit, stderr)
	}
	if !strings.HasPrefix(stdout, "CONTROL OK ") || !strings.Contains(stdout, "kind=stop") || !strings.Contains(stdout, "override=true") {
		t.Errorf("CONTROL OK must name the kind and the override, got: %s", stdout)
	}

	// The stopped card is no longer open work, and the log says WHY it closed:
	// a control event of kind=stop with its reason, not work done.
	exit, stdout, stderr = b.run(b.board("list", "--stale", "10m")...)
	if exit != 0 {
		t.Fatalf("list exit %d: %s", exit, stderr)
	}
	if got := field(stdout, "open="); got != "0" {
		t.Errorf("a stopped card is not open work: open=%s in\n%s", got, stdout)
	}
	raw, err := os.ReadFile(filepath.Join(b.dir, distributed+".board"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "control kind=stop reason=the release is cut; stop the migration") {
		t.Errorf("the log must record the control event with its kind and reason, got:\n%s", raw)
	}

	// A closed card has nothing running left to interrupt: a second stop is a NO.
	exit, _, stderr = b.run(b.board("control", "--as", "rowan", "--card", distributed, "--stale", "10m",
		"--kind", "stop", "--reason", "stop it again")...)
	if exit != 1 {
		t.Fatalf("control over a closed card: exit %d, want 1: %s", exit, stderr)
	}

	// --------------------------------------------------- correct an issued task
	// A correction invalidates the issued revision; the corrected task is a new
	// card naming the old one (the board's standing supersession rule).
	issued := b.add(plain("rowan", "ship the release on the old schema")...)
	exit, stdout, stderr = b.run(b.board("control", "--as", "rowan", "--card", issued, "--stale", "10m",
		"--kind", "correct", "--reason", "the schema moved; invalidate the old plan")...)
	if exit != 0 {
		t.Fatalf("control correct exit %d: %s", exit, stderr)
	}
	if !strings.Contains(stdout, "kind=correct") {
		t.Errorf("CONTROL OK must name kind=correct, got: %s", stdout)
	}
	corrected := b.add(plain("rowan", "ship the release on the new schema (corrects "+issued+")")...)

	// ---------------------------------------------------------- reprioritize
	// A priority change is recorded with its own kind, and the target scope is
	// the one card: unrelated parallel work is never touched.
	unrelated := b.add(plain("emma", "the windows runner skips three steps")...)
	exit, stdout, stderr = b.run(b.board("control", "--as", "rowan", "--card", corrected, "--stale", "10m",
		"--kind", "priority", "--reason", "the release goes first")...)
	if exit != 0 {
		t.Fatalf("control priority exit %d: %s", exit, stderr)
	}
	if !strings.Contains(stdout, "kind=priority") {
		t.Errorf("CONTROL OK must name kind=priority, got: %s", stdout)
	}

	// Unrelated work continues: emma's card is still open and was never touched.
	exit, stdout, stderr = b.run(b.board("list", "--stale", "10m", "--list", "--open")...)
	if exit != 0 {
		t.Fatalf("list exit %d: %s", exit, stderr)
	}
	if n := count(stdout, "BOARD CARD "); n != 1 || !strings.Contains(stdout, unrelated) {
		t.Errorf("exactly the unrelated card stays open: %d BOARD CARD lines in\n%s", n, stdout)
	}

	// ------------------------------------------- the kinds are distinguished
	// priority, correct, pause, cancel and stop are the five; anything else is
	// not a control event this tool will write.
	exit, _, stderr = b.run(b.board("control", "--as", "rowan", "--card", unrelated, "--stale", "10m",
		"--kind", "whatever", "--reason", "no such kind")...)
	if exit != 2 {
		t.Fatalf("an unknown kind: exit %d, want 2: %s", exit, stderr)
	}
}
