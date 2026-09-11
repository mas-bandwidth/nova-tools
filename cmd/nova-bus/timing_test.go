//go:build perf

// THE WALL-CLOCK GUARD, which is not a per-commit test and says so in its build tag.
//
// Why it is behind `perf`: this file is the one test in the package that asserts a WALL
// TIME, and a wall-clock bound is a flake by construction -- it passes or fails on what
// else the machine is doing. Measured 2026-09-11: alone it takes eleven seconds, and under
// the load of `go test ./...` the same run took one minute fifty-one and the package hit
// Go's ten-minute timeout with it (#63). The house rule is that a per-commit test finishes
// in a minute, two at most, or it becomes nightly; a test that fails because a sibling
// package was compiling has stopped being evidence about this tool.
//
// What did NOT move: the property the spec states. `nova-bus`'s claim is that a read costs
// the size of the CHANGE, and SPEC.md proves it by COUNTING PARSES -- exactly 1 over ten
// thousand notes with five hundred open -- in TestInboxParsesOnlyWhatIsNewSinceTheCursor,
// which runs on every commit and is untouched. A count is exact, it is the same on every
// machine, and it measures work not done. This file is the crude second net UNDER that one,
// for a regression that is not a parse: a walk of every lane's INDEX per note, a git call
// per open entry, a quadratic string build. Those show up only as the tool getting slow on
// a big bus, which is a thing no count can see -- so the net is kept, in the one place a
// wall clock belongs.
//
// How to run it -- the TAG, never a test name, which is the whole lesson of this file: a
// second wall-clock test was added below and a -run naming the first one ran it nowhere.
//
//	go test -tags perf -count=1 -p 1 -parallel 1 ./...
//
// `-p 1 -parallel 1` because these are wall clocks and the rest of the suite is parallel
// now: a bound in seconds measured beside a dozen siblings spawning git processes measures
// the siblings. .github/workflows/ci.yml's `perf` job runs exactly that on a nightly
// schedule and on workflow_dispatch.

package main

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// THE GUARD AGAINST A REGRESSION IN THE THING THIS TOOL IS FOR, and it is deliberately the
// crudest test in the repo.
//
// The complexity property is proved properly elsewhere, by COUNTING PARSES (see
// cursor_test.go): a count is exact, it is the same on every machine, and it measures work
// not done, which is what O(new) is a claim about. Nothing here replaces that. What a parse
// count cannot see is a regression that is not a parse -- a walk of every lane's INDEX per
// note, a git call per open entry, a quadratic string build -- and those show up as one
// thing only: the tool gets slow on a big bus. So this runs every verb over the
// ten-thousand-note fixture and holds each to a bound.
//
// The bound is a WALL CLOCK, which makes it the one test here that can flake, so it is
// treated accordingly: it is generous by more than an order of magnitude against what the
// reading verbs actually take (tens of milliseconds against a second), it is skipped under
// the race detector, whose instrumentation would be what it measured, and its failure
// message says what it means -- a verb that has gone from milliseconds to a second on a
// bus this size has stopped being the size of the change, and the parse counts are where
// to look next.
const timingBound = time.Second

// pushBound is the bound for the two verbs that also FETCH, COMMIT and PUSH.
//
// They measure at around 650ms against a second, which is not a generous bound, and almost
// none of that is this tool: it is git reading a ten-thousand-entry index for the status
// check, writing a tree over it, and running a fetch and a push against the remote. A bound
// that tight over work this tool does not do is a bound that fails on a slower disk and
// teaches nobody anything, so these get their own, stated here rather than hidden by
// loosening the one above -- which is the number that actually guards the read path.
const pushBound = 3 * time.Second

func TestEveryVerbIsUnderASecondOnTenThousandNotes(t *testing.T) {
	if raceEnabled {
		t.Skip("a wall-clock bound under the race detector measures the instrumentation")
	}
	if testing.Short() {
		t.Skip("builds a ten-thousand-note fixture")
	}
	hermetic(t)
	checkout, _ := busDir(t)

	const history = 10000
	const carried = 500
	var index strings.Builder
	for i := range history {
		id := fmt.Sprintf("bo-%012x", i+0x100000)
		path := fmt.Sprintf("from-bo/2026-08-%02dT%02d%02dZ-bulk-%s.md", i%28+1, i/60%24, i%60, id[len(id)-12:])
		to := "Bo"
		if i < carried {
			to = "Ada"
		}
		writeFile(t, checkout, path, fmt.Sprintf(
			"From: Bo\nTo: %s\nDate: Sat Aug %2d 00:00:00 UTC 2026\nId: %s\nSubject: bulk %d\n\nA note in the history.\n",
			to, i%28+1, id, i))
		fmt.Fprintf(&index, "%s\t%s\t2026-08-%02dT00:00:00Z\t%s\t-\n", id, path, i%28+1, to)
	}
	appendFile(t, checkout, "from-bo/INDEX", index.String())
	commitAs(t, checkout, "Bo", "ten thousand notes")

	// The one full read a reader ever pays for, which gives Ada a cursor and an open list
	// of five hundred. It is NOT timed: a full walk is the size of the bus by definition
	// and always was, which is why the cursor exists.
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0)

	// One new note, so every incremental verb below has something to find.
	writeFile(t, checkout, "from-bo/2026-09-08T0900Z-one-more-222222222222.md",
		"From: Bo\nTo: Ada\nDate: Tue Sep  8 09:00:00 UTC 2026\nId: bo-222222222222\nSubject: One more\n\nIs the gate on the merge queue?\n")
	appendFile(t, checkout, "from-bo/INDEX",
		"bo-222222222222\tfrom-bo/2026-09-08T0900Z-one-more-222222222222.md\t2026-09-08T09:00:00Z\tAda\t-\n")
	commitAs(t, checkout, "Bo", "one more")
	head := strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD~1"))

	timed := func(name string, bound time.Duration, args ...string) {
		t.Helper()
		start := time.Now()
		r := invoke(t, draftFrom("Ada", "Timing "+name, "One note, over ten thousand."), args...)
		took := time.Since(start)
		if r.code != 0 {
			t.Fatalf("%s: exit %d\nstdout: %s\nstderr: %s", name, r.code, r.stdout, r.stderr)
		}
		if took > bound {
			t.Fatalf("%s took %s over a bus of %d notes with %d open, past the %s bound; this verb should be the size of the CHANGE, so something now walks the record -- the parse counts in cursor_test.go are where to look",
				name, took, history, carried, bound)
		}
		t.Logf("%s: %s", name, took)
	}

	timed("names", timingBound, "names", "--bus", checkout)
	timed("inbox", timingBound, "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40")
	timed("inbox --open", timingBound, "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--open")
	timed("check --since", timingBound, "check", "--bus", checkout, "--since", head)
	timed("check --as", timingBound, "check", "--bus", checkout, "--as", "Ada")
	timed("inbox --advance", pushBound, advance(checkout, "Ada")...)
	timed("receipt", pushBound, "receipt", "--bus", checkout, "--as", "Ada", "--note", "bo-222222222222",
		"--remote", "origin", "--branch", "main")
	timed("send", pushBound, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main")
}

// The loop property of `wait`, which used to live in wait_test.go and is a wall clock: it
// says that more than one poll fits inside a two-second deadline at a hundred-millisecond
// interval, and that is a statement about how fast a git fetch is on this machine. Under
// the load of `go test ./...` on 2026-09-11 it was false -- one fetch took the whole two
// seconds, the run reported polls=1, and the test failed over a tool that had done nothing
// wrong (#63).
//
// It is a real property: a verb that returns "nothing yet" after a single look is a check
// and not a wait, and polls= is the only place that difference is visible. So it is kept,
// here, on a quiet machine at night, and what stays per-commit in wait_test.go is that the
// run polled at least once, reported the count, did not return before its deadline, printed
// no listing and wrote nothing -- every part of the promise that does not depend on the
// runner.
func TestAWaitPollsMoreThanOnceBeforeItsDeadline(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	settled(t, checkout)

	const timeout = 2 * time.Second
	r := invoke(t, "", waitFlags(checkout, "Ada", timeout.String())...).mustCode(t, 0)
	r.mustContain(t, "stdout", "WAIT TIMEOUT after=")

	line := r.stdout[strings.Index(r.stdout, "WAIT TIMEOUT"):]
	polls := field(t, line, "polls=")
	n, err := strconv.Atoi(polls)
	if err != nil || n < 2 {
		t.Fatalf("polls=%q, want at least 2 over %s at 100ms; a wait that looks once is a check\n%s", polls, timeout, r.stdout)
	}
	t.Logf("polls=%d over %s at 100ms", n, timeout)
}
