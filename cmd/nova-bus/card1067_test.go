package main

import (
	"strings"
	"testing"
	"time"
)

// card-1067 / #674: a wait that returns because a To: note arrived prints exactly
// WAIT OK and the INBOX NOTE line for the note that woke it, and nothing else --
// no INBOX OPEN carrying= line, no INBOX OK. The OPEN frame moves behind inbox --open.
//
// BEFORE the fix this test fails: inboxListing prints the OPEN carrying= line on
// every call, and waitPoll hands that line straight to stdout on a keep() return.
func TestAWakeOnNotePrintsWaitOkAndNoteOnlyNoInboxOpen(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	// Ada is settled up to the bus's two old notes; the wait will block on a quiet bus.
	settled(t, checkout)

	other := bench(t, bare)
	note(t, other, "bo-987654321098", "the note that wakes the wait")
	pushed := make(chan error, 1)
	go func() {
		time.Sleep(250 * time.Millisecond)
		pushed <- push(other)
	}()

	args := []string{
		"wait", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--timeout", "30s", "--interval", "100ms",
		"--remote", "origin", "--branch", "main", "--attempts", "3",
	}
	r := invoke(t, "", args...).mustCode(t, 0)
	if err := <-pushed; err != nil {
		t.Fatal(err)
	}

	r.mustContain(t, "stdout", "WAIT OK new=1").
		mustContain(t, "stdout", "INBOX NOTE id=bo-987654321098")
	if strings.Contains(r.stdout, "INBOX OPEN") {
		t.Fatalf("a plain wait return printed the INBOX OPEN frame; the #674 contract says a To: wake prints WAIT OK and INBOX NOTE only:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout, "WAIT TIMEOUT") {
		t.Fatalf("a default wait that saw a new To: note timed out:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "WAIT DONE reason=new") {
		t.Fatalf("the wait's terminal line is missing; --quiet-beats and the new contract both keep WAIT DONE reason=new:\n%s", r.stdout)
	}
}

// card-1067 / #674: when a caller asks for the backlog with --open, the OPEN frame
// is the one place it prints. inbox --open DOES print INBOX OPEN carrying=<n>.
//
// BEFORE the fix this test passes: inboxListing already prints the OPEN carrying=
// line on every inbox call. It is here so the green run proves --open still prints
// the frame after the fix narrows the gate.
func TestInboxOpenStillPrintsTheOpenFrameForTheCarryingList(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	// Advance Ada once so the next inbox is an incremental read with a non-empty open list;
	// the test is about what --open does to the OPEN frame, not about the carry size.
	invoke(t, "", advance(checkout, "Ada", "--open")...).mustCode(t, 0)

	args := []string{
		"inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--open",
	}
	r := invoke(t, "", args...).mustCode(t, 0)

	r.mustContain(t, "stdout", "INBOX SCOPE").
		mustContain(t, "stdout", "INBOX OPEN carrying=")

	if n := strings.Count(r.stdout, "INBOX OPEN carrying="); n != 1 {
		t.Fatalf("inbox --open printed %d INBOX OPEN carrying= lines, want exactly 1:\n%s", n, r.stdout)
	}
	if !strings.Contains(r.stdout, "INBOX OK as=Ada") {
		t.Fatalf("inbox --open dropped the INBOX OK line that names the decomposed counts:\n%s", r.stdout)
	}
}
