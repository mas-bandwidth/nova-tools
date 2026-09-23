package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// nova-tools #2178: `wait --on-note` empty-tick, rearm and refusal behaviour.
// Spec: docs/SPEC-BUS.md lines 85-87, covers behaviours 6 7 13 14.
//
// --on-note without its required flags is refused (exit 2, remedy on stderr, no stdout).
// --on-note with --open or --full is refused (exit 2, remedy on stderr, no stdout).
// An empty-tick under --on-note prints one WAIT TIMEOUT line, prints no INBOX OPEN frame,
// and exits with the rearm line so the harness can restart.

// TestIssue2178 is the single anchor test that reproduces nova-tools#2178:
// it fails on base-sha, passes at head, and fails again when the production change is reverted.
func TestIssue2178(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	t.Run("refuses on-note without required flags", func(t *testing.T) {
		// --on-note without --timeout: refused
		r := invoke(t, "", "wait", "--on-note", "--bus", checkout, "--as", "Ada",
			"--remote", "origin", "--branch", "main")
		if r.code != 2 {
			t.Errorf("exit = %d, want 2", r.code)
		}
		if !strings.Contains(r.stderr, "nova-bus wait: --on-note needs --timeout") {
			t.Errorf("stderr does not name the missing flag:\n%s", r.stderr)
		}
		if r.stdout != "" {
			t.Errorf("a refusal printed to stdout:\n%s", r.stdout)
		}
	})

	t.Run("refuses on-note with --open", func(t *testing.T) {
		r := invoke(t, "", "wait", "--on-note", "--bus", checkout, "--as", "Ada",
			"--timeout", "1s", "--remote", "origin", "--branch", "main", "--open")
		if r.code != 2 {
			t.Errorf("exit = %d, want 2", r.code)
		}
		if !strings.Contains(r.stderr, "nova-bus wait: --on-note prints no open frame") {
			t.Errorf("stderr does not refuse --open with --on-note:\n%s", r.stderr)
		}
		if r.stdout != "" {
			t.Errorf("a refusal printed to stdout:\n%s", r.stdout)
		}
	})
}

// onNoteFlags is `wait --on-note` as a unit runs it: every flag the refusal above requires,
// and none of --open or --full.
func onNoteFlags(checkout, who, timeout string) []string {
	return []string{
		"wait", "--on-note", "--bus", checkout, "--as", who, "--receipt-max-words", "40",
		"--timeout", timeout, "--interval", "100ms",
		"--remote", "origin", "--branch", "main", "--attempts", "3",
	}
}

// ccNote writes and commits one note from Bo To: Dana with Ada only on Cc:, and does NOT
// push it. It is on Ada's listing as data (addr=cc) and is never her wake (SPEC-BUS:
// "the wake being To: only").
func ccNote(t *testing.T, dir, id, subject string) {
	t.Helper()
	path := fmt.Sprintf("from-bo/2026-09-09T1300Z-%s-%s.md", strings.ReplaceAll(subject, " ", "-"), id[len(id)-12:])
	writeFile(t, dir, path, fmt.Sprintf(
		"From: Bo\nTo: Dana\nCc: Ada\nDate: Wed Sep  9 13:00:00 UTC 2026\nId: %s\nSubject: %s\n\nFor Dana; Ada is copied.\n", id, subject))
	appendFile(t, dir, "from-bo/INDEX", fmt.Sprintf("%s\t%s\t2026-09-09T13:00:00Z\tDana;Ada\t-\n", id, path))
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "bo: "+subject)
}

// Stella's hold 6 on #3368, first control: under --on-note a tick that brings no note
// addressed To: the waiter is EMPTY, even when the ordinary predicate (r.New > 0) would
// call it news. Here the only new note copies Ada on Cc:. The wait prints nothing for it,
// does not return on it, and ends on its deadline with WAIT TIMEOUT and the rearm line.
func TestWaitOnNoteEmptyTickPrintsNothingAndDoesNotReturn(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: waits out a real wall-clock timeout; runs on the self-hosted legs and nightly")
	}
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	settled(t, checkout)

	other := bench(t, bare)
	ccNote(t, other, "bo-cc0000000001", "copied only")
	if err := push(other); err != nil {
		t.Fatal(err)
	}

	const timeout = 400 * time.Millisecond
	r := invoke(t, "", onNoteFlags(checkout, "Ada", timeout.String())...).mustCode(t, 0)
	r.mustContain(t, "stdout", "WAIT TIMEOUT after=").
		mustContain(t, "stdout", "WAIT DONE reason=timeout rearm=required next=")
	if after := afterOf(t, r.stdout); after < timeout {
		t.Fatalf("wait --on-note returned after %s, before its %s deadline, on a note that only copies the waiter:\n%s", after, timeout, r.stdout)
	}
	for _, line := range strings.Split(strings.TrimRight(r.stdout, "\n"), "\n") {
		if !strings.HasPrefix(line, "WAIT as=") && !strings.HasPrefix(line, "WAIT TIMEOUT ") && !strings.HasPrefix(line, "WAIT DONE ") {
			t.Fatalf("an empty --on-note tick printed %q; it prints nothing:\n%s", line, r.stdout)
		}
	}
}

// Stella's hold 6 on #3368, second control: a note addressed To: the waiter arriving
// mid-wait ends the wait with that note -- the WAIT OK id= status line, the note's INBOX
// NOTE line and its body -- and no INBOX SCOPE/OPEN/OK frame and no carrying count.
func TestWaitOnNoteReturnsTheArrivingNoteWithoutTheOpenFrame(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	settled(t, checkout)

	other := bench(t, bare)
	note(t, other, "bo-0e0000000002", "for Ada")
	pushed := pushAtSyncPoint(t, checkout, other)

	r := invoke(t, "", onNoteFlags(checkout, "Ada", "20s")...).mustCode(t, 0)
	if err := <-pushed; err != nil {
		t.Fatal(err)
	}
	path := "from-bo/2026-09-09T1300Z-for-Ada-0e0000000002.md"
	r.mustContain(t, "stdout", "WAIT OK id=bo-0e0000000002 from=Bo path="+path+" bytes=").
		mustContain(t, "stdout", "INBOX NOTE id=bo-0e0000000002 from=Bo addr=to ").
		mustContain(t, "stdout", "INBOX BODY id=bo-0e0000000002 bytes=").
		mustContain(t, "stdout", "Is the gate on the merge queue?").
		mustContain(t, "stdout", "INBOX BODY END id=bo-0e0000000002")
	for _, frame := range []string{"INBOX SCOPE", "INBOX OPEN", "INBOX OK", "carrying=", "WAIT OK new=", "WAIT TIMEOUT"} {
		if strings.Contains(r.stdout, frame) {
			t.Fatalf("wait --on-note printed %q; it returns the note and no inbox/open frame:\n%s", frame, r.stdout)
		}
	}
}
