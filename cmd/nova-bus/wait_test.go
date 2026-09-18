package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

// A wait is one read, and every return ends with one terminal line that says it ended and
// hands back the command to re-arm it. A background process is not a harness wake: the
// harness wakes when the call RETURNS, and the caller must then issue the next wait. The
// line is always last, so a harness reading the tail of the transcript finds it.
func TestWaitEndsWithRearmLine(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: waits out a real wall-clock timeout; runs on the self-hosted legs and nightly")
	}
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	settled(t, checkout)

	r := invoke(t, "", waitFlags(checkout, "Ada", "1s")...).mustCode(t, 0)

	done := "WAIT DONE reason=timeout rearm=required next=nova-bus wait"
	if !strings.Contains(r.stdout, done) {
		t.Fatalf("wait return is missing the terminal re-arm line:\n%s", r.stdout)
	}
	trimmed := strings.TrimRight(r.stdout, "\n")
	last := trimmed[strings.LastIndex(trimmed, "\n")+1:]
	// The expected --bus word is built with the binary's OWN shellQuote, never spelled
	// out here. A hard-coded bare path made this assertion an assertion about the
	// PLATFORM: a darwin temporary directory holds nothing shellQuote acts on, so the
	// bare spelling matched, while a Windows temporary path -- backslashes and the
	// RUNNER~1 tilde -- comes back single-quoted and the same line read as "not last".
	// The line under test is that the re-arm line is LAST; what one argument looks like
	// quoted is TestRearmCommandQuotesArgumentsWithSpaces's.
	if !strings.HasPrefix(last, "WAIT DONE reason=timeout rearm=required next=nova-bus wait --bus "+shellQuote(checkout)) {
		t.Fatalf("the re-arm line is not last:\n%s", r.stdout)
	}
}

// THE POINT OF THE VERB: a note pushed by somebody else, mid-call, ends the wait. The
// caller is inside a tool call the whole time and gets the listing the moment it is true.
func TestWaitReturnsWhenANoteArrivesDuringTheWait(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	settled(t, checkout)

	other := bench(t, bare)
	note(t, other, "bo-333333333333", "mid wait")

	// Pushed at the SYNC POINT and not after a sleep: the hook fires the moment the wait
	// has polled, found nothing and is about to sleep, so the note cannot land before the
	// wait is waiting and the wait cannot return before it lands. A sleep raced the first
	// poll, and the deadline then had to be long enough for the race to come out right
	// (#370). That order is the assertion, and a channel makes it rather than a clock.
	pushed := pushAtSyncPoint(t, checkout, other)

	r := invoke(t, "", waitFlags(checkout, "Ada", "30s")...).mustCode(t, 0)
	if err := <-pushed; err != nil {
		t.Fatal(err)
	}

	r.mustContain(t, "stdout", "WAIT as=Ada timeout=30s interval=100ms cursor=").
		mustContain(t, "stdout", "WAIT OK new=1").
		mustContain(t, "stdout", "INBOX SCOPE mode=since").
		mustContain(t, "stdout", "INBOX NOTE id=bo-333333333333").
		mustContain(t, "stdout", "INBOX OK as=Ada")
	// It returned ON THE NOTE and not on its deadline. `WAIT OK` above and the absence of
	// `WAIT TIMEOUT` here are the whole of that claim: the tool says which of the two ended
	// it, so the test does not need to time the call to know.
	if strings.Contains(r.stdout, "WAIT TIMEOUT") {
		t.Fatalf("the wait timed out over a note that arrived:\n%s", r.stdout)
	}
	// The polls before the note are silent: a wait that printed a listing per poll would
	// be a poller with extra steps, and the caller's transcript is what this verb is for.
	if n := strings.Count(r.stdout, "INBOX SCOPE"); n != 1 {
		t.Fatalf("the run printed %d listings, want exactly the one it returned on:\n%s", n, r.stdout)
	}
}

// THE DEFAULT INTERVAL IS TEN SECONDS, and it is pinned here because it is a number
// somebody chose rather than a number that fell out. Glenn, watching two lines answer each
// other through this verb: "the polling should be 10 sec". A poll is a git fetch, and the
// interval is what stands between one line writing a note and the other one seeing it, so
// it is chosen for the round trip and not for the fetch.
//
// It is asserted on the WAIT line, which is where a caller who named no interval is told
// what they got. --timeout is short: what is under test is the number, not the sleeping.
func TestTheDefaultWaitIntervalIsTenSeconds(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: waits out a real wall-clock timeout; runs on the self-hosted legs and nightly")
	}
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	settled(t, checkout)

	invoke(t, "", "wait", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--timeout", "1s", "--remote", "origin", "--branch", "main").
		mustCode(t, 0).
		mustContain(t, "stdout", "WAIT as=Ada timeout=1s interval=10s cursor=").
		mustContain(t, "stdout", "WAIT TIMEOUT after=")
}

// Nothing arrives: the wait stops when it said it would, says so, and exits 0. A timeout
// is the answer "nothing yet", not an error -- the caller issues the next one.
func TestWaitTimesOutQuietlyAndCountsItsPolls(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: waits out a real wall-clock timeout; runs on the self-hosted legs and nightly")
	}
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	settled(t, checkout)

	// The deadline is SHORT, because nothing here is timed by this test any more. The two
	// seconds this used to take were bought to make `time.Since(start)` tell the deadline
	// apart from the work around it -- and that comparison is gone: the tool reports its
	// own `after=`, so the claim "it did not come back early" is read out of the line the
	// tool printed rather than measured by a harness the scheduler can park. What is left
	// is real sleeping and nothing else, so it is cut to what proves the verb sleeps.
	const timeout = 300 * time.Millisecond
	r := invoke(t, "", waitFlags(checkout, "Ada", timeout.String())...).mustCode(t, 0)

	r.mustContain(t, "stdout", "WAIT as=Ada timeout=300ms interval=100ms cursor=").
		mustContain(t, "stdout", "WAIT TIMEOUT after=")
	if strings.Contains(r.stdout, "INBOX ") {
		t.Fatalf("a wait that found nothing printed a listing:\n%s", r.stdout)
	}
	if after := afterOf(t, r.stdout); after < timeout {
		t.Fatalf("the wait says it returned after %s, before its %s deadline:\n%s", after, timeout, r.stdout)
	}
	// It polled and said how many times: a tool that returns "nothing" without saying it
	// looked is indistinguishable from one that did not look.
	//
	// AT LEAST ONE, and not at least two, which is what this asserted until 2026-09-11.
	// "More than one poll in two seconds at a hundred milliseconds" is a claim about how
	// fast a git fetch is on the machine running the test, and under the load of `go test
	// ./...` it is false: one fetch took the whole two seconds and the run reported
	// polls=1, correctly (#63). That claim is a wall clock wearing a count's clothes, so it
	// did not get deleted -- it moved to TestAWaitPollsMoreThanOnceBeforeItsDeadline in
	// timing_test.go, behind `-tags perf`, with every other assertion here that depends on
	// what else the machine is doing. What is left is the part that is true on any machine:
	// the run looked, and it said so.
	line := r.stdout[strings.Index(r.stdout, "WAIT TIMEOUT"):]
	polls := field(t, line, "polls=")
	if n, err := strconv.Atoi(polls); err != nil || n < 1 {
		t.Fatalf("polls=%q, want at least 1 over %s at 100ms:\n%s", polls, timeout, r.stdout)
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
//
// ONE line, and it is the LISTING's. A forward-drawn date is the shape with a remedy, and
// the listing a wait prints already carries that remedy in full -- INBOX SWITCH, with the
// command in it. `wait` saying the same thing again in a sentence without the command
// would be two lines about one line, which is the noise this is meant to end.
func TestWaitReturnsAtOnceWhenTheCursorsLineHidesTheWholeWait(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	// Tomorrow, on the fixed clock: the line a reader draws when they mean "from today".
	invoke(t, "", advance(checkout, "Ada", "--legacy-before", "2026-09-10")...).mustCode(t, 0)

	r := invoke(t, "", waitFlags(checkout, "Ada", "30s")...).mustCode(t, 0)

	r.mustContain(t, "stdout", "INBOX SWITCH your switch-day line is the date 2026-09-10, which hides every note dated 2026-09-09 or earlier; draw it at an instant, once: nova-bus inbox --bus ").
		mustContain(t, "stdout", "--legacy-now --advance --remote \"origin\" --branch \"main\"").
		mustContain(t, "stdout", "WAIT OK new=0").
		mustContain(t, "stdout", "INBOX LEGACY before=2026-09-10")
	if strings.Contains(r.stdout, "WAIT NOTE") {
		t.Fatalf("the wait said the listing's sentence a second time, without the command:\n%s", r.stdout)
	}
	if n := strings.Count(r.stdout, "your switch-day line"); n != 1 {
		t.Fatalf("the line drawn forward was mentioned %d times, want 1:\n%s", n, r.stdout)
	}
	// "At once" is `WAIT OK` above and the absence of `WAIT TIMEOUT` here, which together
	// say the call returned on the first poll and not on its thirty-second deadline. The
	// ten-second wall clock that used to stand here said the same thing in a way that a
	// loaded runner could make false.
	if strings.Contains(r.stdout, "WAIT TIMEOUT") {
		t.Fatalf("the wait sat out its timeout behind a line that hides everything:\n%s", r.stdout)
	}
	if polls := pollsOf(t, r.stdout, "WAIT OK"); polls != 1 {
		t.Fatalf("the wait polled %d times to say the line hides everything; it is meant to say so on the first:\n%s", polls, r.stdout)
	}
}

// AND THE CASE WITH NO CANNED REMEDY, which is what WAIT NOTE is left for. A line drawn
// forward as an INSTANT was set to the second by somebody who meant a moment, so there is
// no command to hand them and inboxListing says nothing about it -- but it hides the whole
// wait exactly as a date does, and a reader owed that fact is owed it whatever shape their
// line is in.
func TestWaitSaysWhyAnInstantDrawnForwardHidesTheWholeWait(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	// This evening, on the fixed clock of 2026-09-09T12:34:56Z: after now, and after
	// anything a thirty-second wait could see.
	invoke(t, "", advance(checkout, "Ada", "--legacy-before", "2026-09-09T18:07:00Z")...).mustCode(t, 0)

	r := invoke(t, "", waitFlags(checkout, "Ada", "30s")...).mustCode(t, 0)

	r.mustContain(t, "stdout", "WAIT NOTE your switch-day line is 2026-09-09T18:07:00Z").
		mustContain(t, "stdout", "a line drawn today has to be an INSTANT").
		mustContain(t, "stdout", "WAIT OK new=0")
	if strings.Contains(r.stdout, "INBOX SWITCH") {
		t.Fatalf("the listing handed a remedy for a line somebody drew to the second:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout, "WAIT TIMEOUT") {
		t.Fatalf("the wait sat out its timeout behind a line that hides everything:\n%s", r.stdout)
	}
}

// A WAIT RETURN, WITHOUT --open, OVER A BACKLOG. This is the loop the README now
// recommends, and the whole of what it prints: the note that woke it, in full, and one
// line for the backlog it did not print. With --open in that loop, a line carrying
// seventy-four re-read all seventy-four on every poll.
func TestAWaitReturnsTheNewNoteInFullAndOneLineForTheBacklog(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	// No switch-day line: Ada is carrying the fixture's two, which is the backlog.
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=2")

	other := bench(t, bare)
	note(t, other, "bo-333333333333", "mid wait")
	// At the sync point, so the note lands while the wait is waiting rather than whenever
	// a 250ms sleep and the first poll happen to fall on the machine running this.
	pushed := pushAtSyncPoint(t, checkout, other)

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
		mustContain(t, "stdout", "INBOX NOTE id=bo-333333333333").
		mustContain(t, "stdout", "INBOX OPEN carrying=3 heard=0")
	// ONE note line: the one that woke it. Not the two it was already carrying.
	if n := strings.Count(r.stdout, "INBOX NOTE ") + strings.Count(r.stdout, "INBOX RECEIPT "); n != 1 {
		t.Fatalf("a wait return without --open printed %d listing lines, want the 1 new note:\n%s", n, r.stdout)
	}
	if n := strings.Count(r.stdout, "INBOX OPEN carrying="); n != 1 {
		t.Fatalf("a wait return printed %d OPEN carrying lines, want exactly 1:\n%s", n, r.stdout)
	}
}

// Issue #328, re-landed: without --advance, wait blocks while there is nothing new,
// rather than spinning in a zero-delay loop, and returns only when a note arrives or
// the deadline passes. A settled reader with nothing new sits out the whole timeout.
func TestWaitWithoutAdvanceBlocksWhenCursorIsUnadvanced(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: waits out a real wall-clock timeout; runs on the self-hosted legs and nightly")
	}
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	settled(t, checkout) // Ada is up to date: nothing new to wake on.

	const timeout = 300 * time.Millisecond
	r := invoke(t, "", waitFlags(checkout, "Ada", timeout.String())...).mustCode(t, 0)

	// The tool's own `after=`, not this test's clock: what is under test is that the verb
	// blocks rather than spinning, and the verb is the thing that knows how long it did.
	if after := afterOf(t, r.stdout); after < timeout {
		t.Fatalf("wait says it returned after %s, before its %s deadline, with nothing new:\n%s", after, timeout, r.stdout)
	}
	r.mustContain(t, "stdout", "WAIT TIMEOUT after=")
	if strings.Contains(r.stdout, "WAIT OK") {
		t.Fatalf("wait without --advance returned WAIT OK with nothing new:\n%s", r.stdout)
	}
}

// A note arriving mid-wait wakes a wait that had nothing new, without --advance. The
// news is what ends the wait; an unadvanced backlog is returned on poll 1 exactly as
// inbox returns it, so the wait that must block is the one over a quiet bus.
func TestWaitWithoutAdvanceReturnsWhenNoteArrivesDuringWaitWithUnadvancedCursor(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	settled(t, checkout) // Ada is up to date: nothing new to wake on.

	other := bench(t, bare)
	note(t, other, "bo-555555555555", "new note during wait")

	pushed := pushAtSyncPoint(t, checkout, other)

	// 30s and not the 5s this had: nothing here asserts an elapsed time, so a deadline
	// costs exactly nothing when the machine is quick and is the only thing standing
	// between this test and a red run when it is not. At 5s the merge group's
	// windows-latest leg reported `WAIT TIMEOUT after=8.364s polls=2` -- the sync point
	// had fired, and the push behind it did not finish inside the deadline.
	r := invoke(t, "", waitFlags(checkout, "Ada", "30s")...).mustCode(t, 0)
	if err := <-pushed; err != nil {
		t.Fatal(err)
	}

	r.mustContain(t, "stdout", "WAIT OK new=1").
		mustContain(t, "stdout", "INBOX NOTE id=bo-555555555555")
	if strings.Contains(r.stdout, "WAIT TIMEOUT") {
		t.Fatalf("the wait timed out over a note pushed at its own sync point:\n%s", r.stdout)
	}
}

// THE SYNC POINT, for every test that needs a note to arrive while a wait is waiting.
//
// A test that pushed after `time.Sleep(250ms)` was racing the first poll, and the whole
// claim then rested on the deadline being long enough for the race to come out right on
// whatever machine ran it. #370 replaced the sleep with a hook the tool calls at the exact
// boundary a wait becomes blocked -- polled, found nothing, about to sleep -- and this is
// that, made reusable and safe to call from a PARALLEL test.
//
// It has to be a registry rather than the plain Swap the first caller used. The hook is one
// process-wide atomic; two parallel tests each swapping their own in and restoring "the
// previous one" on the way out is the lost-update bug, and the loser's wait then sits out
// its whole deadline with nobody to push to it. So ONE dispatcher is installed for the
// process and every caller registers its own checkout under it.
//
// The returned channel carries the push's error, once. The caller reads it after the wait
// returns, so a push that failed is a test failure and never a silent timeout.
func pushAtSyncPoint(t *testing.T, checkout, other string) <-chan error {
	t.Helper()
	installWaitSyncPoint()

	blocked := make(chan struct{})
	var once sync.Once
	syncPoints.mu.Lock()
	syncPoints.at[checkout] = func() { once.Do(func() { close(blocked) }) }
	syncPoints.mu.Unlock()
	t.Cleanup(func() {
		syncPoints.mu.Lock()
		delete(syncPoints.at, checkout)
		syncPoints.mu.Unlock()
	})

	pushed := make(chan error, 1)
	go func() {
		<-blocked
		pushed <- push(other)
	}()
	return pushed
}

// afterOf is how long the TOOL says a wait took, read off its own closing line.
//
// The claim "this wait did not come back before its deadline" used to be made with
// time.Since around the call, and that is the harness measuring the harness: a runner that
// parks the test goroutine after the call returns inflates it, and one that parks it before
// the call starts is the flake in the other direction. `after=` is the verb's own number,
// taken between its own start and its own return, so the assertion is about the verb.
func afterOf(t *testing.T, stdout string) time.Duration {
	t.Helper()
	i := strings.Index(stdout, "WAIT TIMEOUT")
	if i < 0 {
		i = strings.Index(stdout, "WAIT OK")
	}
	if i < 0 {
		t.Fatalf("no WAIT TIMEOUT or WAIT OK line to read after= from:\n%s", stdout)
	}
	d, err := time.ParseDuration(field(t, stdout[i:], "after="))
	if err != nil {
		t.Fatalf("after= is not a duration: %v\n%s", err, stdout)
	}
	return d
}

// pollsOf is how many polls the tool says a wait made, off the line that ended it.
func pollsOf(t *testing.T, stdout, line string) int {
	t.Helper()
	i := strings.Index(stdout, line)
	if i < 0 {
		t.Fatalf("no %s line to read polls= from:\n%s", line, stdout)
	}
	n, err := strconv.Atoi(field(t, stdout[i:], "polls="))
	if err != nil {
		t.Fatalf("polls= is not a number: %v\n%s", err, stdout)
	}
	return n
}

// syncPoints is the registry: one entry per checkout a test is waiting on.
var syncPoints = struct {
	mu sync.Mutex
	at map[string]func()
}{at: map[string]func(){}}

var syncPointOnce sync.Once

// installWaitSyncPoint puts the one dispatcher in place, once for the process. It is never
// removed: with no entry for a bus dir it does nothing, so a test that has finished costs
// a map lookup and a wait nobody registered for is untouched.
func installWaitSyncPoint() {
	syncPointOnce.Do(func() {
		hook := waitBlockedHook(func(dir string) {
			syncPoints.mu.Lock()
			fire := syncPoints.at[dir]
			syncPoints.mu.Unlock()
			if fire != nil {
				fire()
			}
		})
		testWaitBlockedHook.Store(&hook)
	})
}

// Issue #328: a coordinator who receipts a note and then waits is a reader whose only news
// is a note they have already heard. Without --advance the wait returns at once on that
// note -- heard is not answered, so it is still news to the open list -- and the caller pays
// a turn for nothing. With --advance the cursor is moved to the head over the heard note,
// one WAIT ADVANCED line says so, and the wait blocks for a genuinely new note instead of
// returning.
func TestWaitAdvanceSkipsHeardNotesAndBlocks(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: waits out a real wall-clock timeout; runs on the self-hosted legs and nightly")
	}
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	settled(t, checkout)

	other := bench(t, bare)
	note(t, other, "bo-555555555555", "a note already receipted")
	if err := push(other); err != nil {
		t.Fatal(err)
	}
	gitIn(t, checkout, "pull", "-q", "--ff-only", "origin", "main")
	invoke(t, "", "receipt", "--bus", checkout, "--as", "Ada", "--note", "bo-555555555555",
		"--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)

	const timeout = 300 * time.Millisecond
	r := invoke(t, "", waitFlags(checkout, "Ada", timeout.String(), "--advance")...).mustCode(t, 0)

	r.mustContain(t, "stdout", "WAIT ADVANCED from=").
		mustContain(t, "stdout", " to=").
		mustContain(t, "stdout", " heard=1").
		mustContain(t, "stdout", "WAIT TIMEOUT after=")
	if strings.Contains(r.stdout, "WAIT OK new=1") {
		t.Fatalf("wait --advance returned WAIT OK on a note it had already receipted:\n%s", r.stdout)
	}
	if after := afterOf(t, r.stdout); after < timeout {
		t.Fatalf("wait --advance says it returned after %s, before its %s deadline, over only a heard note:\n%s", after, timeout, r.stdout)
	}
	// The cursor moved over the heard note: its commit is the one this run read to, which
	// is the parent of the cursor commit the advance itself made.
	//
	// The cursor commit is found by the FILE it wrote rather than as HEAD~1. Both name the
	// same commit and the claim is the same one; HEAD~1 additionally assumed that the
	// advance's commit is the last thing the wait commits, and it is not: this wait goes on
	// polling for the rest of its second after the advance, and a wait lands its own beat
	// on the way out so that the friend's next verb gets a clean checkout (#488). Asserting
	// through the file says what this test is about and nothing about what follows it.
	cursorCommit := strings.TrimSpace(gitIn(t, checkout, "log", "-1", "--format=%H", "--", "from-ada/CURSOR"))
	readTo := strings.TrimSpace(gitIn(t, checkout, "rev-parse", cursorCommit+"~1"))
	if onLane := strings.Fields(read(t, checkout, "from-ada/CURSOR")); len(onLane) == 0 || onLane[0] != readTo {
		t.Fatalf("the cursor was not advanced to head over the heard note (read to %s):\n%s", readTo, read(t, checkout, "from-ada/CURSOR"))
	}
}

// THE BEAT IS WRITTEN EVERY TICK. A waiting line's cursor does not move -- there was
// nothing to read -- so a line whose cursor never moves reads asleep to `nova-wake awake`.
// The BEAT is the file that moves anyway: rewritten on every poll, one line, the newest
// stamp and the cursor the line is standing at. The write is unbounded; what is bounded is
// the push, tested next. Here --beat is far longer than the wait, so nothing is pushed and
// the only trace is the working-tree file, rewritten down to its last tick.
func TestWaitWritesBeatEachTick(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: waits out a real wall-clock timeout; runs on the self-hosted legs and nightly")
	}
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	settled(t, checkout)

	invoke(t, "", waitFlags(checkout, "Ada", "1s", "--beat", "1h")...).mustCode(t, 0)

	// Rewritten, not appended: one line, an RFC 3339 UTC stamp, the cursor sha, and a
	// lease until=<stamp>.
	beat := strings.TrimSpace(read(t, checkout, "from-ada/BEAT"))
	fields := strings.Fields(beat)
	if len(fields) != 3 {
		t.Fatalf("BEAT is %q, want one line <stamp> <cursor> until=<stamp>", beat)
	}
	if _, err := time.Parse(time.RFC3339Nano, fields[0]); err != nil {
		t.Fatalf("BEAT stamp %q is not an RFC 3339 UTC stamp: %v", fields[0], err)
	}
	cursor := strings.Fields(read(t, checkout, "from-ada/CURSOR"))
	if len(cursor) == 0 || fields[1] != cursor[0] {
		t.Fatalf("BEAT cursor %q does not match CURSOR %q", fields[1], read(t, checkout, "from-ada/CURSOR"))
	}
	if strings.Count(beat, "\n") != 0 {
		t.Fatalf("BEAT is more than one line (rewritten, not appended):\n%q", beat)
	}
}

// THE BEAT CARRIES A LEASE, and it is written on exit as well as on every tick. A wait
// that returns hands the harness the note and then is done: between that return and the
// next wait there is a gap where the manager process is alive but no beat is written, and
// over a slow note the gap outruns --window and the line reads asleep to `nova-wake
// awake`. So the exit beat extends until=now+--beat-lease out over that gap, and the
// lease is what keeps a working manager cycle reading awake.
func TestWaitWritesLeaseOnExit(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: waits out a real wall-clock timeout; runs on the self-hosted legs and nightly")
	}
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	settled(t, checkout)

	invoke(t, "", waitFlags(checkout, "Ada", "1s", "--beat", "1h")...).mustCode(t, 0)

	beat := strings.TrimSpace(read(t, checkout, "from-ada/BEAT"))
	fields := strings.Fields(beat)
	if len(fields) != 3 || !strings.HasPrefix(fields[2], "until=") {
		t.Fatalf("BEAT is %q, want <stamp> <cursor> until=<stamp>", beat)
	}
	stamp, err := time.Parse(time.RFC3339Nano, fields[0])
	if err != nil {
		t.Fatalf("BEAT stamp %q is not an RFC 3339 UTC stamp: %v", fields[0], err)
	}
	until, err := time.Parse(time.RFC3339Nano, strings.TrimPrefix(fields[2], "until="))
	if err != nil {
		t.Fatalf("BEAT until %q is not an RFC 3339 UTC stamp: %v", fields[2], err)
	}
	if d := until.Sub(stamp); d != 10*time.Minute {
		t.Fatalf("the exit beat's lease is %s, want the 10m default: %q", d, beat)
	}
}

// THE PUSH IS BOUNDED. A beat push costs somebody's server, so only the push is gated at
// --beat, never the write: over a wait whose --beat is far longer than --interval the BEAT
// is written on every poll but pushed only when a whole beat has elapsed. Here the beat is
// short enough that a push MUST happen, and the assertion is that pushes never outrun the
// polls -- a beat pushed once per poll would be a poller, not a beat.
func TestWaitBeatPushBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: waits out a real wall-clock timeout; runs on the self-hosted legs and nightly")
	}
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	settled(t, checkout)

	r := invoke(t, "", waitFlags(checkout, "Ada", "1s", "--beat", "150ms")...).mustCode(t, 0)

	pollCount, err := strconv.Atoi(field(t, r.stdout[strings.Index(r.stdout, "WAIT TIMEOUT"):], "polls="))
	if err != nil || pollCount < 1 {
		t.Fatalf("polls=%d, want at least 1:\n%s", pollCount, r.stdout)
	}

	log := gitIn(t, checkout, "log", "--format=%s", "main")
	beats := 0
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, "beat ada") {
			beats++
		}
	}
	if beats < 1 {
		t.Fatalf("a wait with --beat 150ms pushed no beat commit:\n%s", log)
	}
	if beats > pollCount {
		t.Fatalf("pushed %d beat commits over %d polls; the push is bounded by --beat, not once per tick:\n%s", beats, pollCount, log)
	}
}

// A beat commit from another line is not a note, so a wait sleeps through it -- with or
// without --quiet-beats. Until #328 (2026-09-17) the flag made a beat a wake worth one WAIT
// line; with six lines beating once a minute that was a poll with extra steps, and every
// wake cost the waiting window a turn. A wake is a note addressed to the reader, nothing
// else; the flag stays accepted so callers that pass it keep working.
func TestWaitQuietBeatsSleepsThroughABeatCommit(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	settled(t, checkout)

	other := bench(t, bare)
	// Bo's beat: a commit touching only from-bo/BEAT, no note.
	writeFile(t, other, "from-bo/BEAT", "2026-09-09T12:35:00Z - until=2026-09-09T12:45:00Z\n")
	gitIn(t, other, "add", "from-bo/BEAT")
	gitIn(t, other, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "beat bo")
	if err := push(other); err != nil {
		t.Fatal(err)
	}

	r := invoke(t, "", waitFlags(checkout, "Ada", "2s", "--quiet-beats")...).mustCode(t, 0)

	r.mustContain(t, "stdout", "WAIT DONE reason=timeout")
	if strings.Contains(r.stdout, "WAIT OK") || strings.Contains(r.stdout, "reason=new") {
		t.Fatalf("a beat-only change woke the wait; a beat is not news:\n%s", r.stdout)
	}
}

// The wait help must document #328, not #674. The flag is accepted and changes
// nothing, so the long paragraph promising a WAIT OK line on a beat-only change is
// stale: the flag's own usage string says so, and the banner must agree. (#903)
func TestWaitHelpQuietBeatsDocumentsNoWake(t *testing.T) {
	t.Parallel()
	banner := invoke(t, "", "help", "wait").mustCode(t, 0).stdout
	if strings.Contains(banner, "makes a wait return on a change that is ONLY beats") {
		t.Fatalf("wait help still promises --quiet-beats wakes a wait:\n%s", banner)
	}
	if !strings.Contains(banner, "accepted and changes nothing") {
		t.Fatalf("wait help does not say --quiet-beats is accepted and changes nothing:\n%s", banner)
	}
}

// A --bus path holding a space must round-trip through the re-arm command: the line's next=
// is shell-quoted argument by argument, so pasting it hands --bus the SAME one argument --
// space and all -- rather than splitting it in two. splitShellWords tokenizes the way a
// shell would for the grammar rearmCommand emits; the emitted command text is never executed.
func TestRearmCommandQuotesArgumentsWithSpaces(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: waits out a real wall-clock timeout; runs on the self-hosted legs and nightly")
	}
	t.Parallel()
	hermetic(t)
	_, bare := busDir(t)
	checkout := filepath.Join(t.TempDir(), "stella 2 bus")
	gitIn(t, filepath.Dir(checkout), "clone", "--quiet", bare, checkout)
	settled(t, checkout)

	args := waitFlags(checkout, "Ada", "1s")
	r := invoke(t, "", args...).mustCode(t, 0)

	trimmed := strings.TrimRight(r.stdout, "\n")
	line := trimmed[strings.LastIndex(trimmed, "\n")+1:]
	cmd, ok := strings.CutPrefix(line, "WAIT DONE reason=timeout rearm=required next=")
	if !ok {
		t.Fatalf("the re-arm line is missing next=:\n%s", r.stdout)
	}
	got := splitShellWords(cmd)
	want := append([]string{"nova-bus", "wait"}, args[1:]...)
	if len(got) != len(want) {
		t.Fatalf("re-arm tokenized to %d words, want %d:\nnext=%s\ngot=%q\nwant=%q", len(got), len(want), cmd, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("word %d is %q, want %q:\nnext=%s", i, got[i], want[i], cmd)
		}
	}
}

// splitShellWords tokenizes one command line under the grammar rearmCommand emits: words
// split on spaces and tabs, single-quoted runs literal, and a backslash outside quotes
// escapes the next character -- the '\” idiom that puts an apostrophe inside single quotes.
// Nothing is executed and nothing is expanded, because the lines under test hold none.
func splitShellWords(line string) []string {
	var words []string
	var cur strings.Builder
	inWord := false
	inQuote := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inQuote:
			if c == '\'' {
				inQuote = false
			} else {
				cur.WriteByte(c)
			}
		case c == ' ' || c == '\t':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		case c == '\'':
			inWord = true
			inQuote = true
		case c == '\\':
			inWord = true
			if i+1 < len(line) {
				i++
				cur.WriteByte(line[i])
			} else {
				cur.WriteByte(c)
			}
		default:
			inWord = true
			cur.WriteByte(c)
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}

// FREDDY'S HARNESS DOES NOT WAKE, and it cannot loop either: it runs one tool call per turn
// and branches on what came back. The three tests below are that harness's whole contract.
//
// --idle-exit is the code a TIMEOUT returns instead of 0, so "nothing arrived" and "a note
// arrived" are two different numbers rather than two shapes of output to parse. The line
// still says so, in the one WAIT TIMEOUT line a harness can grep, because an exit code that
// appears nowhere in the transcript is a number somebody reads a bug into.
func TestWaitIdleExitGivesATimeoutItsOwnCode(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: waits out a real wall-clock timeout; runs on the self-hosted legs and nightly")
	}
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	settled(t, checkout)

	r := invoke(t, "", waitFlags(checkout, "Ada", "300ms", "--idle-exit", "3")...).mustCode(t, 3)
	r.mustContain(t, "stdout", "WAIT as=Ada timeout=300ms interval=100ms cursor=").
		mustContain(t, "stdout", "idle-exit=3").
		mustContain(t, "stdout", "WAIT TIMEOUT after=").
		mustContain(t, "stdout", "WAIT DONE reason=timeout")
	// ONE line to grep, and the code is on it.
	line := r.stdout[strings.Index(r.stdout, "WAIT TIMEOUT"):]
	if got := field(t, line, "idle-exit="); got != "3" {
		t.Fatalf("WAIT TIMEOUT says idle-exit=%q, want 3:\n%s", got, r.stdout)
	}
}

// A NOTE IS STILL EXIT 0 with --idle-exit set: the flag names what a TIMEOUT returns and
// nothing else. A harness that got 3 for a note would answer nothing and re-arm for ever.
func TestWaitIdleExitDoesNotTouchAReturnOnANote(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	// No cursor, so the fixture's two notes are news and the first poll returns at once.
	invoke(t, "", waitFlags(checkout, "Ada", "30s", "--idle-exit", "3")...).
		mustCode(t, 0).
		mustContain(t, "stdout", "WAIT OK new=2").
		mustContain(t, "stdout", "INBOX NOTE id=bo-abcdef012345")
}

// --until IS THE DEADLINE THE HARNESS ALREADY HAS: a MOMENT, not a duration. It stands
// beside --timeout and the earlier of the two ends the wait, so a caller whose session ends
// at a known instant does not have to work out how long is left and does not overshoot it.
func TestWaitUntilIsAnAbsoluteDeadlineAndTheEarlierOneWins(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: waits out a real wall-clock timeout; runs on the self-hosted legs and nightly")
	}
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	settled(t, checkout)

	// One second past the fixed clock these tests run on, against a --timeout of thirty:
	// the instant is the earlier of the two and is therefore the one that ends the call.
	until := now().Add(time.Second).Format(time.RFC3339)
	r := invoke(t, "", waitFlags(checkout, "Ada", "30s", "--until", until)...).mustCode(t, 0)
	r.mustContain(t, "stdout", "until="+until).
		mustContain(t, "stdout", "WAIT TIMEOUT after=")
	after := afterOf(t, r.stdout)
	if after < time.Second {
		t.Fatalf("the wait returned after %s, before the --until it was given:\n%s", after, r.stdout)
	}
	// Well under the --timeout it was also given: the two are not added and the longer one
	// does not win.
	if after > 15*time.Second {
		t.Fatalf("the wait ran %s against --until %s and --timeout 30s; the instant did not bound it:\n%s", after, until, r.stdout)
	}
}

// The invocations this verb refuses rather than guesses at. Every one of them is a harness
// that would otherwise be told something untrue about its own deadline or its own exit code.
func TestWaitRefusesAnUnusableUntilOrIdleExit(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	cases := []struct {
		name  string
		extra []string
		want  string
	}{
		{"an --until that is not an instant", []string{"--until", "tomorrow"}, "is not an RFC 3339 instant"},
		{"an --until already past", []string{"--until", "2026-09-09T12:00:00Z"}, "is now or in the past"},
		{"--idle-exit 1, a refusal's code", []string{"--idle-exit", "1"}, "is this tool's own code"},
		{"--idle-exit 2, an invocation's code", []string{"--idle-exit", "2"}, "is this tool's own code"},
		{"--idle-exit above the shell's floor", []string{"--idle-exit", "126"}, "belong to the shell"},
		{"a negative --idle-exit", []string{"--idle-exit", "-1"}, "is not an exit code this verb will use"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			invoke(t, "", waitFlags(checkout, "Ada", "300ms", c.extra...)...).
				mustCode(t, 2).mustContain(t, "stderr", c.want)
		})
	}
}
