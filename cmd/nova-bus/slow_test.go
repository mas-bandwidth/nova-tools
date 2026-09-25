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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// SLOW: 5.3 s on hetzner at dev 64b9bec48, over the five-second line.
// TestAStaleCursorCostsTheBoundAndNotTheDistance is the second half of card 9376: the walk
// itself, measured.
//
// THE FAILURE, on the bus this was written for: `inbox` over a cursor 285 commits stale,
// carrying 2150 notes, ran four minutes and printed nothing new. The bound above stops the
// READ, and until this test it did not stop the ASKING: `rev-list --count <cursor>..HEAD`
// walks every commit between the two ends before it can answer, so the one run that reads
// nothing was still paying for the whole distance in order to be told not to read it.
//
// So the numbers here are COUNTS and never a clock -- bus.CommitsWalked, taken where the
// commits are enumerated, and bus.NoteParses, taken where a note is opened. A wall-clock
// assertion over a fixture this size is a flake on a shared runner and proves nothing on a
// fast enough machine; a count of work not done is the only honest proof there is.
//
// PARALLEL, for the reason the two tests in cursor_test.go are: both counts are read for
// THIS test's bus alone, with bus.CommitsWalkedIn and bus.NoteParsesIn, so a sibling
// walking its own bus at the same time moves neither, and every assertion below is still
// an exact number.
func TestAStaleCursorCostsTheBoundAndNotTheDistance(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("slow: builds a thousand-commit, two-thousand-note fixture; runs on the self-hosted legs and nightly")
	}
	hermetic(t)
	const commits = 1000
	const notes = 2000
	checkout, base := longBusOfNotes(t, commits, notes)
	// The stale cursor: the commit the fixture stood at, a thousand commits and two
	// thousand notes ago. It is written and not committed, as longBus writes its own -- a
	// cursor commit of its own would stand one commit above the import and every count below
	// would be one out.
	writeFile(t, checkout, "from-ada/CURSOR", base+" 2026-09-09T12:00:00Z open=0\n")

	// BEFORE: what the question cost when it was asked without its bound. The distance is
	// the whole thousand, and every one of them is walked to say so -- on the live bus, 285
	// commits for a read that then did nothing.
	mark := bus.CommitsWalkedIn(checkout)
	distance, err := bus.CommitsBetween(checkout, base, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	unbounded := bus.CommitsWalkedIn(checkout) - mark
	if distance != commits || unbounded != int64(commits) {
		t.Fatalf("the unbounded count answered %d after walking %d commits, want %d and %d; the fixture is not what this test thinks it is", distance, unbounded, commits, commits)
	}

	// AFTER: the same stale cursor, read by the verb. The default bound is 500, so git stops
	// one commit past it -- 501 and not 1000 -- and the run opens NO note at all: the bound
	// is hit before anything is read, which is the whole of what the line says.
	parses := bus.NoteParsesIn(checkout)
	mark = bus.CommitsWalkedIn(checkout)
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 0).
		mustContain(t, "stderr", `INBOX WALK bounded commits=500 remedy="raise --max-commits or close --before <instant>"`)
	if got := bus.CommitsWalkedIn(checkout) - mark; got != defaultMaxCommits+1 {
		t.Fatalf("a bounded read walked %d commits over a cursor %d behind, want %d: the count is not asked with its bound", got, commits, defaultMaxCommits+1)
	}
	if got := bus.NoteParsesIn(checkout) - parses; got != 0 {
		t.Fatalf("a bounded read parsed %d notes, want 0: nothing is read past the bound", got)
	}

	// The read the caller asks for by raising the bound: the thousand commits, once, and
	// every one of the two thousand notes they carried, once each. Nothing is walked twice.
	parses = bus.NoteParsesIn(checkout)
	mark = bus.CommitsWalkedIn(checkout)
	invoke(t, "", advance(checkout, "Ada", "--max-commits", "2000")...).mustCode(t, 0).
		mustContain(t, "stdout", fmt.Sprintf("INBOX OPEN carrying=%d", notes))
	if got := bus.CommitsWalkedIn(checkout) - mark; got != commits {
		t.Fatalf("the allowed walk crossed %d commits, want the %d since the cursor", got, commits)
	}
	if got := bus.NoteParsesIn(checkout) - parses; got != notes {
		t.Fatalf("the allowed walk parsed %d notes, want the %d it was handed", got, notes)
	}

	// AND THE CARRYING SET IS NEVER RE-READ. The reader now carries two thousand notes; the
	// bus has moved by exactly one commit, the reader's OWN cursor commit that the advance
	// made. So the next read crosses that one commit and opens NO note: a cursor commit
	// carries state files and no note, and an open entry carries its own display line, so a
	// backlog costs nothing until somebody asks for it with --full.
	parses = bus.NoteParsesIn(checkout)
	mark = bus.CommitsWalkedIn(checkout)
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 0).
		mustContain(t, "stdout", fmt.Sprintf("INBOX OPEN carrying=%d", notes))
	if got := bus.CommitsWalkedIn(checkout) - mark; got != 1 {
		t.Fatalf("a read at the head walked %d commits, want the 1 the advance's own cursor commit is", got)
	}
	if got := bus.NoteParsesIn(checkout) - parses; got != 0 {
		t.Fatalf("a read carrying %d notes parsed %d of them, want 0: the carrying set is re-read only on --full", notes, got)
	}
}

// SLOW: 1.6 s on hetzner at dev 64b9bec48, a deadline/wedge/wall bound proved by waiting it out.
// THE DEFAULT INTERVAL IS TEN SECONDS, and it is pinned here because it is a number
// somebody chose rather than a number that fell out. Glenn, watching two lines answer each
// other through this verb: "the polling should be 10 sec". A poll is a git fetch, and the
// interval is what stands between one line writing a note and the other one seeing it, so
// it is chosen for the round trip and not for the fetch.
//
// It is asserted on the WAIT line, which is where a caller who named no interval is told
// what they got. --timeout is short: what is under test is the number, not the sleeping.
func TestTheDefaultWaitIntervalIsTenSeconds(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("slow: waits out a real wall-clock timeout; runs on the self-hosted legs and nightly")
	}

	hermetic(t)
	checkout, _ := busDir(t)
	settled(t, checkout)

	invoke(t, "", "wait", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--timeout", "1s", "--remote", "origin", "--branch", "main").
		mustCode(t, 0).
		mustContain(t, "stdout", "WAIT as=Ada timeout=1s interval=10s cursor=").
		mustContain(t, "stdout", "WAIT TIMEOUT after=")
}

// SLOW: 1.5 s on hetzner at dev 64b9bec48, a deadline/wedge/wall bound proved by waiting it out.
// FREDDY'S HARNESS DOES NOT WAKE, and it cannot loop either: it runs one tool call per turn
// and branches on what came back. The three tests below are that harness's whole contract.
//
// --idle-exit is the code a TIMEOUT returns instead of 0, so "nothing arrived" and "a note
// arrived" are two different numbers rather than two shapes of output to parse. The line
// still says so, in the one WAIT TIMEOUT line a harness can grep, because an exit code that
// appears nowhere in the transcript is a number somebody reads a bug into.
func TestWaitIdleExitGivesATimeoutItsOwnCode(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("slow: waits out a real wall-clock timeout; runs on the self-hosted legs and nightly")
	}

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

// SLOW: 1.9 s on hetzner at dev 64b9bec48, a deadline/wedge/wall bound proved by waiting it out.
// --until IS THE DEADLINE THE HARNESS ALREADY HAS: a MOMENT, not a duration. It stands
// beside --timeout and the earlier of the two ends the wait, so a caller whose session ends
// at a known instant does not have to work out how long is left and does not overshoot it.
func TestWaitUntilIsAnAbsoluteDeadlineAndTheEarlierOneWins(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("slow: waits out a real wall-clock timeout; runs on the self-hosted legs and nightly")
	}

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

// SLOW: 5.4 s on hetzner at dev 64b9bec48, over the five-second line.
// TWO BENCHES OF ONE LANE, RACING. This is the shape that wedged a bench: both append to
// the lane's INDEX in the same commit as their note, so the rebase conflicts, and a person's
// own `git pull --rebase` then landed in a half-done rebase with `UU from-<lane>/INDEX`.
// Twenty sends, ten rounds of two, and every one of them lands.
func TestTwoClonesOfOneLaneRacingTenRoundsAllLand(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("slow: spawns twenty racing sends in a loop; runs on the self-hosted legs and nightly")
	}

	hermetic(t)
	checkout, bare := busDir(t)
	second := cloneOf(t, bare)

	const rounds = 10
	benches := []string{checkout, second}
	for round := range rounds {
		var wg sync.WaitGroup
		results := make([]result, len(benches))
		for i, dir := range benches {
			wg.Add(1)
			go func(i int, dir string) {
				defer wg.Done()
				subject := fmt.Sprintf("Round %d from bench %d", round, i)
				body := fmt.Sprintf("Bench %d, round %d, sent at once with the other.", i, round)
				results[i] = invoke(t, draftFrom("Ada", subject, body),
					"send", "--bus", dir, "--stdin", "--remote", "origin", "--branch", "main")
			}(i, dir)
		}
		wg.Wait()
		for i, r := range results {
			if r.code != 0 {
				t.Fatalf("round %d, bench %d: exit %d\nstdout: %s\nstderr: %s", round, i, r.code, r.stdout, r.stderr)
			}
		}
	}

	// 20/20 on the bus.
	files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main")
	if n := strings.Count(files, "from-ada/2026-09-09T1234Z-round-"); n != rounds*2 {
		t.Fatalf("%d of %d notes reached the bus:\n%s", n, rounds*2, files)
	}
	// And the catalogue names every one of them: the union kept both sides' lines every
	// time, rather than one bench's replacing the other's.
	index := gitIn(t, bare, "show", "main:from-ada/INDEX")
	if n := strings.Count(index, "from-ada/2026-09-09T1234Z-round-"); n != rounds*2 {
		t.Fatalf("the catalogue holds %d of %d lines:\n%s", n, rounds*2, index)
	}
	if strings.Contains(index, "<<<") {
		t.Fatalf("conflict markers reached the catalogue:\n%s", index)
	}

	// BOTH TREES ARE CLEAN AND NEITHER IS MID-REBASE, which is the whole of "no conflict
	// wedges a line": a bench a person has to rescue is a bench that lost.
	for i, dir := range benches {
		if branch := strings.TrimSpace(gitIn(t, dir, "rev-parse", "--abbrev-ref", "HEAD")); branch != "main" {
			t.Fatalf("bench %d is on %q, not a branch a person can work from", i, branch)
		}
		if out := gitIn(t, dir, "status", "--porcelain"); strings.TrimSpace(out) != "" {
			t.Fatalf("bench %d was left dirty:\n%s", i, out)
		}
		gd := strings.TrimSpace(gitIn(t, dir, "rev-parse", "--absolute-git-dir"))
		for _, name := range []string{"rebase-merge", "rebase-apply"} {
			if _, err := os.Stat(filepath.Join(gd, name)); !os.IsNotExist(err) {
				t.Fatalf("bench %d was left in a rebase (%s)", i, name)
			}
		}
	}
	// The bus itself is still valid.
	invoke(t, "", "check", "--bus", checkout, "--full").mustCode(t, 0).mustContain(t, "stdout", "BUS OK")
	// And the union rule is on the bus, so a person's own `git pull --rebase` gets the
	// same settlement this tool gave itself.
	attrs := gitIn(t, bare, "show", "main:"+bus.AttributesName)
	for _, want := range []string{"from-*/INDEX merge=union", "from-*/RECEIPTS merge=union"} {
		if !strings.Contains(attrs, want) {
			t.Fatalf("%s does not carry %q:\n%s", bus.AttributesName, want, attrs)
		}
	}
}
