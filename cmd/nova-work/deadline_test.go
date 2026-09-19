package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// The deadline is the client's own bound on one exchange. An ordinary exchange
// keeps the 30-second default; an explicit --deadline bounds it; a declared
// wait keeps its declared wait plus a bounded transport allowance; and a
// deadline already past refuses before anything is dialled. These four are the
// ruling's cases (Stella, 2026-09-19T1428Z item 6).

func TestAnExpiredDeadlineRefusesBeforeAnythingIsDialled(t *testing.T) {
	socket, requests := fakeSession(t, sessionOKLine)
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)

	var stdout, stderr bytes.Buffer
	code := run([]string{"operation", "cancel", "--session", socket, "--deadline", past}, &stdout, &stderr, "")
	if code != 2 {
		t.Fatalf("expired deadline exit = %d, want 2 (stderr = %s)", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expired deadline wrote stdout: %q", stdout.String())
	}
	line := strings.TrimSuffix(stderr.String(), "\n")
	if strings.Count(stderr.String(), "\n") != 1 || !strings.HasSuffix(line, "run: nova-work help") {
		t.Fatalf("expired deadline refusal = %q, want one line ending \"run: nova-work help\"", stderr.String())
	}
	if !strings.Contains(line, "is not after") {
		t.Fatalf("expired deadline refusal = %q, want it naming \"is not after\"", line)
	}
	// A short timed receive, not awaitRequest: nothing may reach the session at
	// all. The grace is a named value rather than a literal so the waits class
	// test reads it as the allowed unknowable bound and not a fixed one.
	const grace = 250 * time.Millisecond
	select {
	case got := <-requests:
		t.Fatalf("the client dialled the session before refusing an expired deadline: %q", got)
	case <-time.After(grace):
	}
}

func TestAFarFutureDeadlineStillGivesUpOnASilentSession(t *testing.T) {
	shortenTheBound(t, 150*time.Millisecond)
	socket := silentSession(t)
	future := time.Now().AddDate(2, 0, 0).UTC().Format(time.RFC3339)

	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run([]string{"operation", "cancel", "--session", socket, "--deadline", future}, &stdout, &stderr, "")
	}()
	var code int
	select {
	case code = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("a far-future deadline turned the ordinary transport bound into an unlimited wait")
	}
	if code != 2 {
		t.Fatalf("silent session with a far-future deadline exit = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("silent session wrote stdout: %q", stdout.String())
	}
	if line := strings.TrimSuffix(stderr.String(), "\n"); !strings.Contains(line, "did not answer") {
		t.Fatalf("far-future deadline silence = %q, want it naming the silence", line)
	}
}

func TestADeclaredWaitKeepsItsTransportAllowance(t *testing.T) {
	// operation wait --timeout T is a Go duration the session is allowed to sit
	// on for T; the client's bound is T plus the transport allowance, so an
	// answer after 30 seconds but before T+30s is no longer a false silence.
	const wait = 90 * time.Second
	if got, want := derivedBound("", wait.String(), ""), wait+30*time.Second; got != want {
		t.Fatalf("the bound for --timeout %s = %s, want %s (the declared wait plus its 30s transport allowance)", wait, got, want)
	}
	// session stop and session handoff spell the same wait as a bare integer of
	// seconds in --git-timeout.
	if got, want := derivedBound("", "", "45"), 75*time.Second; got != want {
		t.Fatalf("the bound for --git-timeout 45 = %s, want %s", got, want)
	}
}

func TestTheDerivedBoundIsTheMinimumAndNeverZero(t *testing.T) {
	soon := time.Now().Add(20 * time.Second).UTC().Format(time.RFC3339)
	at, err := time.Parse(time.RFC3339, soon)
	if err != nil {
		t.Fatalf("parse the deadline fixture: %v", err)
	}
	until := time.Until(at)

	ordinary := derivedBound("", "", "")
	deadlineOnly := derivedBound(soon, "", "")
	declaredOnly := derivedBound("", "45s", "")
	both := derivedBound(soon, "45s", "")

	if ordinary != askTimeout {
		t.Fatalf("the ordinary bound = %s, want askTimeout %s", ordinary, askTimeout)
	}
	if deadlineOnly <= 0 || deadlineOnly >= askTimeout {
		t.Fatalf("the deadline-only bound = %s, want a positive bound under askTimeout %s", deadlineOnly, askTimeout)
	}
	if deadlineOnly > until {
		t.Fatalf("the deadline-only bound = %s, past the %s left on the deadline", deadlineOnly, until)
	}
	if want := 75 * time.Second; declaredOnly != want {
		t.Fatalf("the declared-only bound = %s, want %s", declaredOnly, want)
	}
	// both is min(declared+allowance, time.Until(deadline)); the two calls
	// measure the clock at two instants, so allow a second of drift.
	if both <= 0 || both > declaredOnly || both-deadlineOnly > time.Second || deadlineOnly-both > time.Second {
		t.Fatalf("the both bound = %s, want the minimum of the deadline %s and the declared wait %s", both, deadlineOnly, declaredOnly)
	}
	for name, got := range map[string]time.Duration{
		"ordinary":      ordinary,
		"deadline-only": deadlineOnly,
		"declared-only": declaredOnly,
		"both":          both,
	} {
		if got <= 0 {
			t.Fatalf("the %s bound = %s; a non-positive bound is unlimited to budget()", name, got)
		}
	}
}

func TestAMalformedDeadlineRefusesBeforeAnythingIsDialled(t *testing.T) {
	socket, requests := fakeSession(t, sessionOKLine)

	var stdout, stderr bytes.Buffer
	code := run([]string{"operation", "cancel", "--session", socket, "--deadline", "not-an-instant"}, &stdout, &stderr, "")
	if code != 2 {
		t.Fatalf("malformed deadline exit = %d, want 2 (stderr = %s)", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("malformed deadline wrote stdout: %q", stdout.String())
	}
	line := strings.TrimSuffix(stderr.String(), "\n")
	if strings.Count(stderr.String(), "\n") != 1 || !strings.HasSuffix(line, "run: nova-work help") {
		t.Fatalf("malformed deadline refusal = %q, want one line ending \"run: nova-work help\"", stderr.String())
	}
	if !strings.Contains(line, "not-an-instant") || !strings.Contains(line, "is not an instant") {
		t.Fatalf("malformed deadline refusal = %q, want it naming \"not-an-instant\" and \"is not an instant\"", line)
	}
	// A short timed receive, not awaitRequest: nothing may reach the session at
	// all. Copy of the expired case's grace shape.
	const grace = 250 * time.Millisecond
	select {
	case got := <-requests:
		t.Fatalf("the client dialled the session before refusing a malformed deadline: %q", got)
	case <-time.After(grace):
	}
}

func TestAnAbsentDeadlineStillSendsAndKeepsTheOrdinaryDefault(t *testing.T) {
	socket, requests := fakeSession(t, sessionOKLine)

	var stdout, stderr bytes.Buffer
	code := run([]string{"operation", "cancel", "--session", socket}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("absent deadline exit = %d, want 0 (stderr = %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "SESSION OK") {
		t.Fatalf("absent deadline stdout = %q, want the reply line", stdout.String())
	}
	request := awaitRequest(t, requests)
	if strings.Contains(request, "--deadline") {
		t.Fatalf("an absent deadline spelled one on the wire: %q", request)
	}
	if got := derivedBound("", "", ""); got != askTimeout {
		t.Fatalf("the ordinary bound = %s, want askTimeout %s", got, askTimeout)
	}
}
