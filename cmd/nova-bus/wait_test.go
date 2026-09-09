package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// `wait` is the verb for a harness that does not wake its session: the polling happens
// INSIDE one tool call, so a session that cannot be woken cannot forget to poll. Every
// test here is about the clock around the listing -- that it returns when something
// arrives, that it stops when it said it would, that it refuses a deadline longer than a
// tool call, and that it does not sit through a whole timeout behind a switch-day line
// that hides everything. What is LISTED is inbox's, tested in main_test.go and
// cursor_test.go, and shared rather than copied: see inboxListing.
//
// The durations here are short on purpose -- `--interval 100ms`, timeouts under a second
// -- because the property under test is the ORDER of events and never a wall time. The one
// timing assertion is that a wait which times out did not return before its deadline,
// which is the whole promise of the flag.

// waitFlags is the invocation the tests share; extra flags follow it.
//
// It carries --open, which is the flag a caller of this verb wants and which `wait`
// honours exactly as `inbox` does: without it the run prints ONE line for what is being
// carried, because a reader carrying five hundred settled notes does not want five hundred
// lines on every poll. A wait returns because there is news, and the caller's next action
// is the note, so the note is listed.
func waitFlags(checkout, who, timeout string, extra ...string) []string {
	return append([]string{
		"wait", "--bus", checkout, "--as", who, "--receipt-max-words", "40",
		"--timeout", timeout, "--interval", "100ms", "--open",
		"--remote", "origin", "--branch", "main", "--attempts", "3",
	}, extra...)
}

// settled gives Ada a cursor with the fixture's two old notes behind a switch-day line, so
// that the waits below are incremental reads with an empty open list -- a reader who is up
// to date, which is the reader a wait is for.
func settled(t *testing.T, checkout string) {
	t.Helper()
	invoke(t, "", advance(checkout, "Ada", "--legacy-before", "2026-09-09")...).
		mustCode(t, 0).mustContain(t, "stdout", "INBOX CURSOR commit=")
}

// bench is a second checkout of the same bare bus: another line, at another desk.
func bench(t *testing.T, bare string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "bench")
	gitIn(t, filepath.Dir(dir), "clone", "--quiet", bare, dir)
	return dir
}

// note writes and commits one note from Bo to Ada, with its INDEX line, and does NOT push
// it: the push is the event a wait is waiting for, and the tests below choose when it
// happens.
func note(t *testing.T, dir, id, subject string) {
	t.Helper()
	path := fmt.Sprintf("from-bo/2026-09-09T1300Z-%s-%s.md", strings.ReplaceAll(subject, " ", "-"), id[len(id)-12:])
	writeFile(t, dir, path, fmt.Sprintf(
		"From: Bo\nTo: Ada\nDate: Wed Sep  9 13:00:00 UTC 2026\nId: %s\nSubject: %s\n\nIs the gate on the merge queue?\n", id, subject))
	appendFile(t, dir, "from-bo/INDEX", fmt.Sprintf("%s\t%s\t2026-09-09T13:00:00Z\tAda\t-\n", id, path))
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "bo: "+subject)
}

// push is git's own push, run WITHOUT the test helper, because it is called from another
// goroutine while the wait under test runs and t.Fatalf may not be. The error comes back
// on a channel and is asserted on the test's own goroutine.
func push(dir string) error {
	out, err := exec.Command("git", "-C", dir, "push", "-q", "origin", "HEAD:refs/heads/main").CombinedOutput()
	if err != nil {
		return fmt.Errorf("push from %s: %v\n%s", dir, err, out)
	}
	return nil
}

// THE POINT OF THE VERB: a note pushed by somebody else, mid-call, ends the wait. The
// caller is inside a tool call the whole time and gets the listing the moment it is true.
func TestWaitReturnsWhenANoteArrivesDuringTheWait(t *testing.T) {
	hermetic(t)
	checkout, bare := busDir(t)
	settled(t, checkout)

	other := bench(t, bare)
	note(t, other, "bo-333333333333", "mid wait")

	pushed := make(chan error, 1)
	go func() {
		time.Sleep(250 * time.Millisecond)
		pushed <- push(other)
	}()

	start := time.Now()
	r := invoke(t, "", waitFlags(checkout, "Ada", "30s")...).mustCode(t, 0)
	took := time.Since(start)
	if err := <-pushed; err != nil {
		t.Fatal(err)
	}

	r.mustContain(t, "stdout", "WAIT as=Ada timeout=30s interval=100ms cursor=").
		mustContain(t, "stdout", "WAIT OK new=1").
		mustContain(t, "stdout", "INBOX SCOPE mode=since").
		mustContain(t, "stdout", "INBOX NOTE id=bo-333333333333").
		mustContain(t, "stdout", "INBOX OK as=Ada")
	if strings.Contains(r.stdout, "WAIT TIMEOUT") {
		t.Fatalf("the wait timed out over a note that arrived:\n%s", r.stdout)
	}
	if took >= 30*time.Second {
		t.Fatalf("the wait took %s, which is its whole timeout; it did not return on the note", took)
	}
	// The polls before the note are silent: a wait that printed a listing per poll would
	// be a poller with extra steps, and the caller's transcript is what this verb is for.
	if n := strings.Count(r.stdout, "INBOX SCOPE"); n != 1 {
		t.Fatalf("the run printed %d listings, want exactly the one it returned on:\n%s", n, r.stdout)
	}
}

// Nothing arrives: the wait stops when it said it would, says so, and exits 0. A timeout
// is the answer "nothing yet", not an error -- the caller issues the next one.
func TestWaitTimesOutQuietlyAndCountsItsPolls(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	settled(t, checkout)

	const timeout = 600 * time.Millisecond
	start := time.Now()
	r := invoke(t, "", waitFlags(checkout, "Ada", timeout.String())...).mustCode(t, 0)
	took := time.Since(start)

	r.mustContain(t, "stdout", "WAIT as=Ada timeout=600ms interval=100ms cursor=").
		mustContain(t, "stdout", "WAIT TIMEOUT after=")
	if strings.Contains(r.stdout, "INBOX ") {
		t.Fatalf("a wait that found nothing printed a listing:\n%s", r.stdout)
	}
	if took < timeout {
		t.Fatalf("the wait returned after %s, before its %s deadline", took, timeout)
	}
	// It polled, more than once, and said how many times: a tool that returns "nothing"
	// without saying it looked is indistinguishable from one that did not look.
	line := r.stdout[strings.Index(r.stdout, "WAIT TIMEOUT"):]
	polls := field(t, line, "polls=")
	if n, err := strconv.Atoi(polls); err != nil || n < 2 {
		t.Fatalf("polls=%q, want at least 2 over %s at 100ms:\n%s", polls, timeout, r.stdout)
	}
	// And it names the cursor it waited from, which is the one it started at: a wait that
	// found nothing writes nothing.
	onLane := strings.Fields(read(t, checkout, "from-ada/CURSOR"))
	if at := field(t, line, "cursor="); len(onLane) == 0 || at != onLane[0] {
		t.Fatalf("WAIT TIMEOUT cursor=%s is not the cursor on the lane:\n%s", at, read(t, checkout, "from-ada/CURSOR"))
	}
}

// The ceiling is about HARNESSES and not about buses: a tool call that runs too long is
// killed with nothing said, so a timeout longer than the limit is not a longer wait.
func TestWaitRefusesATimeoutLongerThanAToolCall(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	r := invoke(t, "", waitFlags(checkout, "Ada", "61m")...).mustCode(t, 2)
	r.mustContain(t, "stderr", "--timeout 1h1m0s is longer than 1h0m0s").
		mustContain(t, "stderr", "ask your harness")
	if r.stdout != "" {
		t.Fatalf("a refused invocation printed to stdout:\n%s", r.stdout)
	}
}

// Every wait has a deadline. One with no deadline is a line that is stuck rather than
// waiting, and nobody outside can tell the two apart.
func TestWaitRefusesWithNoTimeoutAtAll(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	invoke(t, "", "wait", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--remote", "origin", "--branch", "main").
		mustCode(t, 2).mustContain(t, "stderr", "--timeout is required")
}

// A wait that cannot fetch is a wait that would sit out its whole timeout beside a bus
// full of notes, so the FIRST poll's fetch failing is a refusal now rather than silence
// for an hour.
func TestWaitRefusesOnTheFirstPollWhenTheFetchFails(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	settled(t, checkout)
	invoke(t, "", "wait", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--timeout", "30s", "--interval", "100ms", "--remote", "nowhere", "--branch", "main").
		mustCode(t, 1).mustContain(t, "stderr", "WAIT REFUSED:")
}

// --advance is inbox's --advance: the same cursor, the same open list, written the same
// way. The two verbs are run over two identical buses and the files compared, because the
// promise is not "it also writes a cursor" -- it is that a line can wait instead of poll
// and nothing else about their reading changes.
func TestWaitAdvancesTheCursorExactlyAsInboxDoes(t *testing.T) {
	hermetic(t)
	byInbox, byWait := "", ""
	openByInbox, openByWait := "", ""
	for _, verb := range []string{"inbox", "wait"} {
		checkout, bare := busDir(t)
		settled(t, checkout)
		other := bench(t, bare)
		note(t, other, "bo-444444444444", "the same note")
		if err := push(other); err != nil {
			t.Fatal(err)
		}
		var r result
		switch verb {
		case "inbox":
			// inbox does not fetch, so the checkout is brought level the way a person or a
			// poller would. That is the difference between the two verbs and the only one.
			gitIn(t, checkout, "pull", "-q", "--ff-only", "origin", "main")
			r = invoke(t, "", advance(checkout, "Ada", "--open")...)
		case "wait":
			r = invoke(t, "", waitFlags(checkout, "Ada", "30s", "--advance")...)
		}
		r.mustCode(t, 0).
			mustContain(t, "stdout", "INBOX NOTE id=bo-444444444444").
			mustContain(t, "stdout", "INBOX CURSOR commit=")
		// The commit the run READ TO, which is the one under the cursor commit it then
		// made: both verbs advance to the head they read, and the cursor is what they
		// commit on top of it.
		readTo := strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD~1"))
		cursor := strings.TrimSpace(read(t, checkout, "from-ada/CURSOR"))
		fields := strings.Fields(cursor)
		if len(fields) < 3 || fields[0] != readTo {
			t.Fatalf("%s wrote CURSOR %q, which does not begin at the commit it read to, %s", verb, cursor, readTo)
		}
		// The stamp is the one field that cannot match: it is when the read happened.
		// Everything else about the cursor is a claim about the BUS and must.
		rest := strings.Join(fields[2:], " ")
		openFile := read(t, checkout, "from-ada/OPEN")
		if verb == "inbox" {
			byInbox, openByInbox = rest, openFile
			continue
		}
		byWait, openByWait = rest, openFile
	}
	if byInbox != byWait {
		t.Fatalf("the cursors differ:\n  inbox: %s\n   wait: %s", byInbox, byWait)
	}
	if openByInbox != openByWait {
		t.Fatalf("the open lists differ:\n  inbox: %q\n   wait: %q", openByInbox, openByWait)
	}
	if !strings.Contains(openByWait, "bo-444444444444") {
		t.Fatalf("neither run put the note on the open list:\n%s", openByWait)
	}
}

// THE LINE THAT HIDES EVERYTHING. A switch-day line given as a DATE is midnight at that
// date's start, so a line of tomorrow's date is a moment after everything anybody writes
// today: every note that arrived during a wait would be history, and the wait would run
// its whole timeout beside a bus that was answering it. The reader is TOLD, on one line,
// and the call returns instead of waiting on a line that can hide nothing else.
func TestWaitReturnsAtOnceWhenTheCursorsLineHidesTheWholeWait(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	// Tomorrow, on the fixed clock: the line a reader draws when they mean "from today".
	invoke(t, "", advance(checkout, "Ada", "--legacy-before", "2026-09-10")...).mustCode(t, 0)

	start := time.Now()
	r := invoke(t, "", waitFlags(checkout, "Ada", "30s")...).mustCode(t, 0)
	took := time.Since(start)

	r.mustContain(t, "stdout", "WAIT NOTE your switch-day line is 2026-09-10").
		mustContain(t, "stdout", "a line drawn today has to be an INSTANT").
		mustContain(t, "stdout", "WAIT OK new=0").
		mustContain(t, "stdout", "INBOX LEGACY before=2026-09-10")
	if strings.Contains(r.stdout, "WAIT TIMEOUT") {
		t.Fatalf("the wait sat out its timeout behind a line that hides everything:\n%s", r.stdout)
	}
	if took > 10*time.Second {
		t.Fatalf("the wait took %s to say the line hides everything; it is meant to say so at once", took)
	}
}
